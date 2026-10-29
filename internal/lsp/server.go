// Package lsp implements the BEM language server.
package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TeddyGavi/bem-ls/internal/bem"
	"github.com/TeddyGavi/bem-ls/internal/config"
	"github.com/TeddyGavi/bem-ls/internal/core"
	"github.com/TeddyGavi/bem-ls/internal/index"
	"github.com/TeddyGavi/bem-ls/internal/lsp/jsonrpc"
	"github.com/TeddyGavi/bem-ls/internal/lsp/protocol"
)

// Server is the language server. Create it with NewServer and call Run.
type Server struct {
	version string
	log     *core.Logger
	conn    *jsonrpc.Conn

	mu              sync.Mutex
	initialized     bool
	shutdown        bool
	root            string
	encoding        string
	caps            clientCaps
	cfg             *compiled
	ix              *index.Index
	open            map[string]*openDoc // by path
	timers          map[string]*time.Timer
	scanCancel      context.CancelFunc
	scanDone        chan struct{}
	initializedDone chan struct{}

	// Settings come from the editor and from bem.config.json at the root;
	// the file wins. Both are kept raw and merged on every change.
	editorRaw  json.RawMessage
	fileRaw    json.RawMessage
	fileErr    error
	configPath string
	configDiag bool // diagnostics are shown on the config file
}

type clientCaps struct {
	documentChanges bool
	createFiles     bool
	markdown        bool
	unnecessaryTag  bool
	watchedFilesDyn bool
	inlineCompl     bool
	// vscode is set for VS Code, which supports client-side commands like
	// editor.action.triggerSuggest in completion items.
	vscode bool
	// configuration: the client answers workspace/configuration.
	configuration bool
}

type openDoc struct {
	uri        protocol.DocumentURI
	version    int
	text       string
	languageID string
}

// NewServer returns a server. logger receives diagnostics about the server
// itself; it must not write to standard output.
func NewServer(version string, logger *core.Logger) *Server {
	if logger == nil {
		logger = core.NewLogger("lsp")
		logger.SetOutput(io.Discard)
	}
	cfg, _ := compile(Settings{})
	return &Server{
		version:         version,
		log:             logger,
		encoding:        protocol.EncodingUTF16,
		cfg:             cfg,
		open:            map[string]*openDoc{},
		timers:          map[string]*time.Timer{},
		initializedDone: make(chan struct{}),
	}
}

// Run serves the protocol on r/w until the client sends exit or closes the
// stream. It returns an error if the client exited without shutdown.
func (s *Server) Run(ctx context.Context, r io.Reader, w io.Writer) error {
	s.conn = jsonrpc.NewConn(r, w, s.handle, s.log)
	err := s.conn.Run(ctx)
	s.stopBackground()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.shutdown {
		return errors.New("exited without shutdown")
	}
	return nil
}

func (s *Server) stopBackground() {
	s.mu.Lock()
	if s.scanCancel != nil {
		s.scanCancel()
	}
	for _, t := range s.timers {
		t.Stop()
	}
	done := s.scanDone
	s.mu.Unlock()
	if done != nil {
		<-done
	}
}

// handle dispatches one message.
func (s *Server) handle(ctx context.Context, req *jsonrpc.Request) (any, error) {
	s.mu.Lock()
	initialized, shutdown := s.initialized, s.shutdown
	s.mu.Unlock()

	switch req.Method {
	case "initialize":
		return s.initialize(req.Params)
	case "exit":
		s.conn.Close()
		return nil, nil
	}
	if !initialized {
		if req.IsNotification() {
			return nil, nil
		}
		return nil, jsonrpc.Errorf(jsonrpc.CodeServerNotInitialized, "server not initialized")
	}
	if shutdown && !req.IsNotification() && req.Method != "shutdown" {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidRequest, "server is shutting down")
	}

	switch req.Method {
	case "initialized":
		s.afterInitialized()
		return nil, nil
	case "shutdown":
		s.mu.Lock()
		s.shutdown = true
		s.mu.Unlock()
		s.stopBackground()
		return nil, nil

	case "textDocument/didOpen":
		var p protocol.DidOpenTextDocumentParams
		return nil, s.decode(req, &p, func() error { return s.didOpen(p) })
	case "textDocument/didChange":
		var p protocol.DidChangeTextDocumentParams
		return nil, s.decode(req, &p, func() error { return s.didChange(p) })
	case "textDocument/didClose":
		var p protocol.DidCloseTextDocumentParams
		return nil, s.decode(req, &p, func() error { return s.didClose(p) })
	case "textDocument/didSave":
		var p protocol.DidSaveTextDocumentParams
		return nil, s.decode(req, &p, func() error {
			if path, err := uriToPath(p.TextDocument.URI); err == nil && s.isConfigFile(path) {
				s.reloadConfigFile()
			}
			return nil
		})
	case "workspace/didChangeWatchedFiles":
		var p protocol.DidChangeWatchedFilesParams
		return nil, s.decode(req, &p, func() error { s.didChangeWatchedFiles(p); return nil })
	case "workspace/didChangeConfiguration":
		var p protocol.DidChangeConfigurationParams
		return nil, s.decode(req, &p, func() error { return s.didChangeConfiguration(p) })

	case "textDocument/hover":
		var p protocol.TextDocumentPositionParams
		return decodeAndCall(req, &p, func() (any, error) { return s.hover(ctx, p) })
	case "textDocument/completion":
		var p protocol.CompletionParams
		return decodeAndCall(req, &p, func() (any, error) { return s.completion(ctx, p) })
	case "textDocument/definition":
		var p protocol.TextDocumentPositionParams
		return decodeAndCall(req, &p, func() (any, error) { return s.definition(ctx, p) })
	case "textDocument/references":
		var p protocol.ReferenceParams
		return decodeAndCall(req, &p, func() (any, error) { return s.references(ctx, p) })
	case "textDocument/prepareRename":
		var p protocol.TextDocumentPositionParams
		return decodeAndCall(req, &p, func() (any, error) { return s.prepareRename(ctx, p) })
	case "textDocument/rename":
		var p protocol.RenameParams
		return decodeAndCall(req, &p, func() (any, error) { return s.rename(ctx, p) })
	case "textDocument/codeAction":
		var p protocol.CodeActionParams
		return decodeAndCall(req, &p, func() (any, error) { return s.codeAction(ctx, p) })
	case "textDocument/documentHighlight":
		var p protocol.TextDocumentPositionParams
		return decodeAndCall(req, &p, func() (any, error) { return s.documentHighlight(ctx, p) })
	case "textDocument/documentSymbol":
		var p protocol.DocumentSymbolParams
		return decodeAndCall(req, &p, func() (any, error) { return s.documentSymbol(ctx, p) })
	case "workspace/symbol":
		var p protocol.WorkspaceSymbolParams
		return decodeAndCall(req, &p, func() (any, error) { return s.workspaceSymbol(ctx, p) })
	case "textDocument/foldingRange":
		var p protocol.FoldingRangeParams
		return decodeAndCall(req, &p, func() (any, error) { return s.foldingRange(ctx, p) })
	case "textDocument/inlineCompletion":
		var p protocol.InlineCompletionParams
		return decodeAndCall(req, &p, func() (any, error) { return s.inlineCompletion(ctx, p) })
	case "workspace/executeCommand":
		var p protocol.ExecuteCommandParams
		return decodeAndCall(req, &p, func() (any, error) { return s.executeCommand(ctx, p) })
	}
	return nil, jsonrpc.ErrMethodNotFound
}

func (s *Server) decode(req *jsonrpc.Request, v any, fn func() error) error {
	if err := json.Unmarshal(req.Params, v); err != nil {
		return jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "%s: %v", req.Method, err)
	}
	return fn()
}

func decodeAndCall(req *jsonrpc.Request, v any, fn func() (any, error)) (any, error) {
	if err := json.Unmarshal(req.Params, v); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "%s: %v", req.Method, err)
	}
	return fn()
}

// ---------------------------------------------------------------------------
// Lifecycle

// Commands the server executes.
const (
	CmdGenerateSkeleton = "bem.generateSkeleton"
)

func (s *Server) initialize(raw json.RawMessage) (any, error) {
	var p protocol.InitializeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "initialize: %v", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initialized {
		return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidRequest, "already initialized")
	}

	root := ""
	switch {
	case len(p.WorkspaceFolders) > 0:
		root, _ = uriToPath(p.WorkspaceFolders[0].URI)
	case p.RootURI != "":
		root, _ = uriToPath(p.RootURI)
	case p.RootPath != "":
		root = p.RootPath
	}
	if root == "" {
		root, _ = os.Getwd()
	}
	s.root = root

	c := p.Capabilities
	if c.General != nil {
		for _, e := range c.General.PositionEncodings {
			if e == protocol.EncodingUTF8 {
				s.encoding = protocol.EncodingUTF8
			}
		}
	}
	if c.Workspace != nil {
		if we := c.Workspace.WorkspaceEdit; we != nil {
			s.caps.documentChanges = we.DocumentChanges
			for _, op := range we.ResourceOperations {
				if op == "create" {
					s.caps.createFiles = true
				}
			}
		}
		if w := c.Workspace.DidChangeWatchedFiles; w != nil {
			s.caps.watchedFilesDyn = w.DynamicRegistration
		}
		s.caps.configuration = c.Workspace.Configuration
	}
	if td := c.TextDocument; td != nil {
		if td.Hover != nil {
			for _, f := range td.Hover.ContentFormat {
				if f == "markdown" {
					s.caps.markdown = true
				}
			}
		}
		if pd := td.PublishDiagnostics; pd != nil && pd.TagSupport != nil {
			for _, v := range pd.TagSupport.ValueSet {
				if v == protocol.TagUnnecessary {
					s.caps.unnecessaryTag = true
				}
			}
		}
		s.caps.inlineCompl = td.InlineCompletion != nil
	}

	if p.ClientInfo != nil {
		s.caps.vscode = strings.Contains(p.ClientInfo.Name, "Visual Studio Code") || strings.Contains(p.ClientInfo.Name, "VSCodium")
	}
	s.editorRaw = p.InitializationOptions
	s.configPath = filepath.Join(s.root, config.FileName)
	s.loadConfigFileLocked()
	cfg, problems := s.buildConfigLocked()
	s.cfg = cfg
	for _, msg := range problems {
		s.notifyLater(protocol.MessageWarning, msg)
	}
	ix, err := index.New(s.root, s.cfg.indexCfg)
	if err != nil {
		return nil, err
	}
	s.ix = ix
	s.initialized = true
	s.log.Infof("initialized: root=%s encoding=%s", s.root, s.encoding)

	name := "bem-ls"
	return protocol.InitializeResult{
		ServerInfo: &protocol.ServerInfo{Name: name, Version: s.version},
		Capabilities: protocol.ServerCapabilities{
			PositionEncoding: s.encoding,
			TextDocumentSync: &protocol.TextDocumentSyncOptions{
				OpenClose: true,
				Change:    protocol.SyncIncremental,
				Save:      &protocol.SaveOptions{},
			},
			HoverProvider: true,
			CompletionProvider: &protocol.CompletionOptions{
				TriggerCharacters: []string{`"`, `'`, "`", " ", ".", "_", "-", "["},
			},
			DefinitionProvider: true,
			ReferencesProvider: true,
			RenameProvider:     &protocol.RenameOptions{PrepareProvider: true},
			CodeActionProvider: &protocol.CodeActionOptions{
				CodeActionKinds: []string{protocol.CodeActionQuickFix, protocol.CodeActionSource},
			},
			ExecuteCommandProvider:  &protocol.ExecuteCommandOptions{Commands: []string{CmdGenerateSkeleton}},
			DocumentSymbolProvider:  true,
			WorkspaceSymbolProvider: true,
			DocumentHighlight:       true,
			FoldingRangeProvider:    true,
			InlineCompletion:        map[string]any{},
		},
	}, nil
}

// notifyLater shows a message once the connection can send (initialize's
// response must go first).
func (s *Server) notifyLater(typ int, msg string) {
	go func() {
		time.Sleep(50 * time.Millisecond)
		s.conn.Notify("window/showMessage", protocol.ShowMessageParams{Type: typ, Message: msg})
	}()
}

func (s *Server) afterInitialized() {
	s.mu.Lock()
	dyn := s.caps.watchedFilesDyn
	scan := s.cfg.settings.Misc.ScanOnStartup == nil || *s.cfg.settings.Misc.ScanOnStartup
	pull := s.caps.configuration && isEmptyJSON(s.editorRaw)
	s.mu.Unlock()
	s.publishConfigDiagnostics()
	if pull {
		go s.pullConfiguration()
	}
	if dyn {
		// Calls must not block the reading goroutine that is running us.
		go func() {
			err := s.conn.Call(context.Background(), "client/registerCapability", map[string]any{
				"registrations": []map[string]any{{
					"id":     "bem-watch",
					"method": "workspace/didChangeWatchedFiles",
					"registerOptions": map[string]any{"watchers": []map[string]any{
						{"globPattern": "**/*.{scss,css,sass}"},
						{"globPattern": "**/*.{tsx,jsx,ts,js}"},
						{"globPattern": "**/" + config.FileName},
					}},
				}},
			}, nil)
			if err != nil {
				s.log.Infof("registering file watchers: %v", err)
			}
		}()
	}
	if scan {
		s.startScan()
	}
	close(s.initializedDone)
}

func (s *Server) startScan() {
	s.mu.Lock()
	if s.scanCancel != nil {
		s.scanCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.scanCancel, s.scanDone = cancel, done
	ix := s.ix
	s.mu.Unlock()
	go func() {
		defer close(done)
		stats, err := ix.Scan(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			s.log.Infof("scan: %v", err)
			return
		}
		s.log.Infof("scan: %d files (%d failed) in %v", stats.Files, stats.Failed, stats.Duration)
		s.conn.Notify("window/logMessage", protocol.LogMessageParams{
			Type:    protocol.MessageLog,
			Message: fmt.Sprintf("BEM: indexed %d files in %v", stats.Files, stats.Duration.Round(time.Millisecond)),
		})
		s.scheduleAll()
	}()
}

// WaitForScan blocks until the background scan finishes (for tests).
func (s *Server) WaitForScan() {
	<-s.initializedDone
	s.mu.Lock()
	done := s.scanDone
	s.mu.Unlock()
	if done != nil {
		<-done
	}
}

// ---------------------------------------------------------------------------
// Document sync

func (s *Server) didOpen(p protocol.DidOpenTextDocumentParams) error {
	path, err := uriToPath(p.TextDocument.URI)
	if err != nil {
		return nil // untitled buffers etc.
	}
	s.mu.Lock()
	s.open[path] = &openDoc{uri: p.TextDocument.URI, version: p.TextDocument.Version, text: p.TextDocument.Text, languageID: p.TextDocument.LanguageID}
	ix := s.ix
	s.mu.Unlock()
	ix.Open(path, p.TextDocument.Version, p.TextDocument.Text)
	s.changed(path)
	return nil
}

func (s *Server) didChange(p protocol.DidChangeTextDocumentParams) error {
	path, err := uriToPath(p.TextDocument.URI)
	if err != nil {
		return nil
	}
	s.mu.Lock()
	od, ok := s.open[path]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("didChange for unopened %s", p.TextDocument.URI)
	}
	od.text = applyChanges(od.text, p.ContentChanges, s.encoding)
	od.version = p.TextDocument.Version
	text, ix := od.text, s.ix
	s.mu.Unlock()
	ix.Change(path, p.TextDocument.Version, text)
	s.changed(path)
	return nil
}

func (s *Server) didClose(p protocol.DidCloseTextDocumentParams) error {
	path, err := uriToPath(p.TextDocument.URI)
	if err != nil {
		return nil
	}
	s.mu.Lock()
	delete(s.open, path)
	if t := s.timers[path]; t != nil {
		t.Stop()
		delete(s.timers, path)
	}
	ix := s.ix
	s.mu.Unlock()
	ix.Close(path)
	s.conn.Notify("textDocument/publishDiagnostics", protocol.PublishDiagnosticsParams{
		URI: p.TextDocument.URI, Diagnostics: []protocol.Diagnostic{},
	})
	s.changed(path)
	return nil
}

func (s *Server) didChangeWatchedFiles(p protocol.DidChangeWatchedFilesParams) {
	s.mu.Lock()
	ix := s.ix
	s.mu.Unlock()
	configChanged := false
	for _, c := range p.Changes {
		if path, err := uriToPath(c.URI); err == nil {
			if s.isConfigFile(path) {
				configChanged = true
				continue
			}
			ix.Invalidate(path)
		}
	}
	if configChanged {
		s.reloadConfigFile()
	}
	s.scheduleAll()
}

func (s *Server) didChangeConfiguration(p protocol.DidChangeConfigurationParams) error {
	s.mu.Lock()
	pull := s.caps.configuration && isEmptyJSON(p.Settings)
	if !pull {
		s.editorRaw = p.Settings
	}
	s.mu.Unlock()
	if pull {
		// Clients using the pull model send an empty notification.
		go s.pullConfiguration()
		return nil
	}
	s.reconfigure()
	return nil
}

// pullConfiguration asks the client for the "bem" section.
func (s *Server) pullConfiguration() {
	var res []json.RawMessage
	err := s.conn.Call(context.Background(), "workspace/configuration", map[string]any{
		"items": []map[string]any{{"section": "bem"}},
	}, &res)
	if err != nil {
		s.log.Infof("workspace/configuration: %v", err)
		return
	}
	if len(res) == 0 {
		return
	}
	s.mu.Lock()
	s.editorRaw = res[0]
	s.mu.Unlock()
	s.reconfigure()
}

func (s *Server) isConfigFile(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return path == s.configPath
}

// loadConfigFileLocked reads bem.config.json. s.mu must be held.
func (s *Server) loadConfigFileLocked() {
	s.fileRaw, s.fileErr = nil, nil
	raw, err := config.LoadFile(s.configPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		s.fileErr = err
		s.log.Infof("config: %v", err)
	default:
		s.fileRaw = raw
		s.log.Infof("config: loaded %s", s.configPath)
	}
}

// buildConfigLocked merges the editor's settings with the file's and
// compiles them. It returns nil if nothing usable came out; problems are
// messages to show. s.mu must be held.
func (s *Server) buildConfigLocked() (*compiled, []string) {
	var problems []string
	if s.fileErr != nil {
		problems = append(problems, "BEM: "+s.fileErr.Error())
	}
	editor, err := config.Parse(s.editorRaw, false)
	if err != nil {
		problems = append(problems, "BEM: ignoring invalid editor settings: "+err.Error())
		s.editorRaw = nil
	}
	merged, err := config.Merge(s.editorRaw, s.fileRaw)
	if err != nil {
		problems = append(problems, "BEM: "+err.Error())
		merged = nil
	}
	settings, err := config.Parse(merged, false)
	if err != nil {
		problems = append(problems, "BEM: "+err.Error())
		settings = editor
	}
	cfg, err := compile(settings)
	if err == nil {
		return cfg, problems
	}
	where := "settings"
	if len(s.fileRaw) > 0 {
		where = config.FileName
	}
	problems = append(problems, fmt.Sprintf("BEM: invalid %s: %v", where, err))
	// Fall back to the editor's settings alone, then to the defaults.
	if cfg, err := compile(editor); err == nil {
		return cfg, problems
	}
	cfg, _ = compile(Settings{})
	return cfg, problems
}

// reloadConfigFile re-reads bem.config.json and applies the result.
func (s *Server) reloadConfigFile() {
	s.mu.Lock()
	s.loadConfigFileLocked()
	s.mu.Unlock()
	s.publishConfigDiagnostics()
	s.reconfigure()
}

// reconfigure rebuilds the settings from both sources and applies them.
func (s *Server) reconfigure() {
	s.mu.Lock()
	cfg, problems := s.buildConfigLocked()
	s.mu.Unlock()
	for _, msg := range problems {
		s.conn.Notify("window/showMessage", protocol.ShowMessageParams{Type: protocol.MessageWarning, Message: msg})
	}
	s.applyConfig(cfg)
}

// publishConfigDiagnostics shows a problem in bem.config.json on the file
// itself, and clears it once fixed.
func (s *Server) publishConfigDiagnostics() {
	s.mu.Lock()
	path, ferr, had, enc := s.configPath, s.fileErr, s.configDiag, s.encoding
	s.configDiag = ferr != nil
	s.mu.Unlock()
	if ferr == nil && !had {
		return
	}
	diags := []protocol.Diagnostic{}
	var fe *config.FileError
	if errors.As(ferr, &fe) {
		r := protocol.Range{}
		if b, err := os.ReadFile(path); err == nil && fe.Offset >= 0 {
			m := newMapper(string(b), enc)
			p := m.position(fe.Offset)
			r = protocol.Range{Start: p, End: p}
		}
		diags = append(diags, protocol.Diagnostic{
			Range: r, Severity: protocol.SeverityError, Source: "bem", Message: fe.Err.Error(),
		})
	}
	s.conn.Notify("textDocument/publishDiagnostics", protocol.PublishDiagnosticsParams{URI: pathToURI(path), Diagnostics: diags})
}

func isEmptyJSON(raw json.RawMessage) bool {
	t := strings.TrimSpace(string(raw))
	return t == "" || t == "null" || t == "{}" || t == "[]" || t == `{"bem":null}` || t == `{"bem":{}}` || t == `{"bem":[]}`
}

// applyConfig switches to new settings. If anything affecting parsing or
// lookup changed, the index is rebuilt and the workspace rescanned.
func (s *Server) applyConfig(cfg *compiled) {
	if cfg == nil {
		return
	}
	s.mu.Lock()
	old := s.cfg
	s.cfg = cfg
	rebuild := !reflect.DeepEqual(old.settings.Stylesheet, cfg.settings.Stylesheet) ||
		!reflect.DeepEqual(old.settings.JSX, cfg.settings.JSX) ||
		!reflect.DeepEqual(old.settings.CSSModules, cfg.settings.CSSModules) ||
		!reflect.DeepEqual(old.settings.Styled, cfg.settings.Styled)
	var docs []*openDoc
	var paths []string
	if rebuild {
		ix, err := index.New(s.root, cfg.indexCfg)
		if err == nil {
			s.ix = ix
			for p, od := range s.open {
				paths = append(paths, p)
				docs = append(docs, od)
			}
		}
	}
	ix := s.ix
	s.mu.Unlock()
	for i, p := range paths {
		ix.Open(p, docs[i].version, docs[i].text)
	}
	if rebuild {
		s.startScan()
	}
	s.scheduleAll()
}

// changed reacts to an edit of path: re-lint it and everything else open,
// since a stylesheet change affects components and vice versa.
func (s *Server) changed(path string) {
	s.schedule(path)
	if index.KindOf(path) == index.KindStylesheet {
		s.scheduleAll()
	}
}

// ---------------------------------------------------------------------------
// Diagnostics

func (s *Server) scheduleAll() {
	s.mu.Lock()
	paths := make([]string, 0, len(s.open))
	for p := range s.open {
		paths = append(paths, p)
	}
	s.mu.Unlock()
	for _, p := range paths {
		s.schedule(p)
	}
}

// schedule publishes diagnostics for an open document after the debounce
// delay; a newer change restarts the delay.
func (s *Server) schedule(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.open[path]; !ok || s.shutdown {
		return
	}
	if t := s.timers[path]; t != nil {
		t.Stop()
	}
	s.timers[path] = time.AfterFunc(s.cfg.delay, func() { s.publish(path) })
}

func (s *Server) publish(path string) {
	s.mu.Lock()
	od, ok := s.open[path]
	if !ok {
		s.mu.Unlock()
		return
	}
	uri, version := od.uri, od.version
	s.mu.Unlock()

	diags := s.diagnostics(path)
	s.mu.Lock()
	// Skip if the document changed meanwhile; a newer publish is coming.
	if cur, ok := s.open[path]; !ok || cur.version != version {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	v := version
	s.conn.Notify("textDocument/publishDiagnostics", protocol.PublishDiagnosticsParams{URI: uri, Version: &v, Diagnostics: diags})
}

// diagData is stored in Diagnostic.Data for code actions.
type diagData struct {
	Rule       bem.Rule `json:"rule"`
	Class      string   `json:"class"`
	Suggestion string   `json:"suggestion,omitempty"`
}

func (s *Server) diagnostics(path string) []protocol.Diagnostic {
	s.mu.Lock()
	ix, cfg, enc, tagOK := s.ix, s.cfg, s.encoding, s.caps.unnecessaryTag
	s.mu.Unlock()
	d, err := ix.Get(path)
	if err != nil {
		return []protocol.Diagnostic{}
	}
	m := newMapper(d.Text, enc)
	var found []bem.Diagnostic
	switch d.Kind {
	case index.KindJSX:
		in, err := ix.LintInput(path)
		if err != nil {
			return []protocol.Diagnostic{}
		}
		found = cfg.linter.Lint(in)
	case index.KindStylesheet:
		found = s.stylesheetDiagnostics(path, cfg)
	}
	out := make([]protocol.Diagnostic, 0, len(found))
	for _, f := range found {
		data, _ := json.Marshal(diagData{Rule: f.Rule, Class: f.Class, Suggestion: f.Suggestion})
		pd := protocol.Diagnostic{
			Range:    m.rangeOf(f.Range),
			Severity: int(f.Severity),
			Code:     string(f.Rule),
			Source:   "bem",
			Message:  f.Message,
			Data:     data,
		}
		if f.Rule == bem.RuleUnusedSelector && tagOK {
			pd.Tags = []int{protocol.TagUnnecessary}
		}
		out = append(out, pd)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Range.Start, out[j].Range.Start
		return a.Line < b.Line || (a.Line == b.Line && a.Character < b.Character)
	})
	return out
}

// ---------------------------------------------------------------------------
// Helpers

// state returns what request handlers need, consistently.
func (s *Server) state() (*index.Index, *compiled, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ix, s.cfg, s.encoding
}

// docAt loads the document for a URI and converts a position to an offset.
func (s *Server) docAt(uri protocol.DocumentURI, pos protocol.Position) (*index.Doc, *mapper, int, error) {
	ix, _, enc := s.state()
	path, err := uriToPath(uri)
	if err != nil {
		return nil, nil, 0, err
	}
	d, err := ix.Get(path)
	if err != nil {
		return nil, nil, 0, err
	}
	m := newMapper(d.Text, enc)
	return d, m, m.offset(pos), nil
}

// location converts an index location, loading the target to map offsets.
func (s *Server) location(l index.Location) (protocol.Location, bool) {
	ix, _, enc := s.state()
	d, err := ix.Get(l.Path)
	if err != nil {
		return protocol.Location{}, false
	}
	return protocol.Location{URI: pathToURI(l.Path), Range: newMapper(d.Text, enc).rangeOf(l.Range)}, true
}

func relPath(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil && !filepath.IsAbs(r) && len(r) < len(p) {
		return filepath.ToSlash(r)
	}
	return p
}

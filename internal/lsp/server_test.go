package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TeddyGavi/bem-ls/internal/core"
	"github.com/TeddyGavi/bem-ls/internal/lsp/jsonrpc"
	"github.com/TeddyGavi/bem-ls/internal/lsp/protocol"
)

var testWorkspace = map[string]string{
	"src/Card/Card.tsx": `import './Card.scss';
import { Title } from './Card.styles';
export const Card = () => (
  <div className="card">
    <Title className="card__title">Hi</Title>
    <p className="card__missing card__Bad" />
  </div>
);
`,
	"src/Card/Card.scss": `.card {
  color: red;

  &__title {
    font-weight: bold;
  }
}
`,
	"src/Card/Card.styles.ts": "import styled from 'styled-components';\nexport const Title = styled.h2`margin: 0;`;\n",
	"src/Profile/Profile.tsx": `import s from './Profile.module.scss';
export const Profile = () => <img className={clsx(s.avatar, s.statsRow)} />;
`,
	"src/Profile/Profile.module.scss": ".avatar {}\n.stats-row {}\n",
	"src/Lonely/Lonely.tsx":           `export const Lonely = () => <div className="lonely"><span className="lonely__text" /></div>;` + "\n",
}

// client is a minimal LSP client driving a Server over pipes.
type client struct {
	t    *testing.T
	root string
	srv  *Server
	w    io.WriteCloser
	done chan error

	mu      sync.Mutex
	nextID  int
	pending map[int]chan rpcReply
	diags   map[protocol.DocumentURI][]protocol.PublishDiagnosticsParams
	diagCh  chan struct{}
	msgs    []string

	// configSection answers workspace/configuration requests.
	configSection json.RawMessage
	configCalls   int
}

// clientOptions customise newClientWith.
type clientOptions struct {
	initOptions   map[string]any
	files         map[string]string // extra files to write
	configuration json.RawMessage   // if set, the client supports workspace/configuration
	noInitOptions bool              // send no initializationOptions at all
}

type rpcReply struct {
	result json.RawMessage
	err    *jsonrpc.Error
}

func newClient(t *testing.T, initOptions map[string]any) *client {
	t.Helper()
	return newClientWith(t, clientOptions{initOptions: initOptions})
}

func newClientWith(t *testing.T, opts clientOptions) *client {
	t.Helper()
	initOptions := opts.initOptions
	root := t.TempDir()
	files := map[string]string{}
	for k, v := range testWorkspace {
		files[k] = v
	}
	for k, v := range opts.files {
		files[k] = v
	}
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	var logger *core.Logger
	if os.Getenv("BEM_LS_TEST_LOG") != "" {
		logger = core.NewLogger("lsp-test")
	} else {
		logger = core.NewLogger("lsp-test")
		logger.SetOutput(io.Discard)
	}

	c := &client{
		t: t, root: root,
		srv:           NewServer("test", logger),
		w:             inW,
		done:          make(chan error, 1),
		pending:       map[int]chan rpcReply{},
		diags:         map[protocol.DocumentURI][]protocol.PublishDiagnosticsParams{},
		diagCh:        make(chan struct{}, 100),
		configSection: opts.configuration,
	}
	go func() {
		c.done <- c.srv.Run(context.Background(), inR, outW)
		outW.Close()
	}()
	go c.readLoop(bufio.NewReader(outR))
	t.Cleanup(func() { inW.Close() })

	if initOptions == nil {
		initOptions = map[string]any{}
	}
	if _, ok := initOptions["misc"]; !ok {
		initOptions["misc"] = map[string]any{"diagnosticsDelay": 5}
	}
	var initOpts any = initOptions
	if opts.noInitOptions {
		initOpts = nil
	}
	var res protocol.InitializeResult
	c.call("initialize", map[string]any{
		"processId": nil,
		"rootUri":   pathToURI(root),
		"capabilities": map[string]any{
			"workspace": map[string]any{"workspaceEdit": map[string]any{
				"documentChanges": true, "resourceOperations": []string{"create"},
			}, "configuration": opts.configuration != nil},
			"textDocument": map[string]any{
				"hover":              map[string]any{"contentFormat": []string{"markdown"}},
				"publishDiagnostics": map[string]any{"tagSupport": map[string]any{"valueSet": []int{1}}},
			},
		},
		"initializationOptions": initOpts,
	}, &res)
	c.notify("initialized", map[string]any{})
	c.srv.WaitForScan()
	return c
}

func (c *client) readLoop(r *bufio.Reader) {
	for {
		n := 0
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			fmt.Sscanf(line, "Content-Length: %d", &n)
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			return
		}
		var msg struct {
			ID     *json.RawMessage `json:"id"`
			Method string           `json:"method"`
			Params json.RawMessage  `json:"params"`
			Result json.RawMessage  `json:"result"`
			Error  *jsonrpc.Error   `json:"error"`
		}
		json.Unmarshal(body, &msg)
		switch {
		case msg.Method == "textDocument/publishDiagnostics":
			var p protocol.PublishDiagnosticsParams
			json.Unmarshal(msg.Params, &p)
			c.mu.Lock()
			c.diags[p.URI] = append(c.diags[p.URI], p)
			c.mu.Unlock()
			c.diagCh <- struct{}{}
		case msg.Method == "workspace/configuration" && msg.ID != nil:
			c.mu.Lock()
			c.configCalls++
			section := c.configSection
			c.mu.Unlock()
			c.send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": []json.RawMessage{section}})
		case msg.Method != "" && msg.ID != nil:
			// A request from the server: accept it.
			c.send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": nil})
		case msg.Method != "":
			c.mu.Lock()
			c.msgs = append(c.msgs, msg.Method+" "+string(msg.Params))
			c.mu.Unlock()
		default:
			var id int
			json.Unmarshal(*msg.ID, &id)
			c.mu.Lock()
			ch := c.pending[id]
			c.mu.Unlock()
			if ch != nil {
				ch <- rpcReply{msg.Result, msg.Error}
			}
		}
	}
}

func (c *client) send(v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n%s", len(b), b)
}

func (c *client) request(method string, params any) rpcReply {
	c.t.Helper()
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan rpcReply, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		c.t.Fatalf("%s: no reply", method)
		return rpcReply{}
	}
}

func (c *client) call(method string, params, result any) {
	c.t.Helper()
	r := c.request(method, params)
	if r.err != nil {
		c.t.Fatalf("%s: %v", method, r.err)
	}
	if result != nil {
		if err := json.Unmarshal(r.result, result); err != nil {
			c.t.Fatalf("%s: decoding %s: %v", method, r.result, err)
		}
	}
}

func (c *client) notify(method string, params any) {
	c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *client) path(rel string) string { return filepath.Join(c.root, filepath.FromSlash(rel)) }
func (c *client) uri(rel string) protocol.DocumentURI {
	return pathToURI(c.path(rel))
}

func (c *client) open(rel string) string {
	b, err := os.ReadFile(c.path(rel))
	if err != nil {
		c.t.Fatal(err)
	}
	c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
		"uri": c.uri(rel), "languageId": "typescriptreact", "version": 1, "text": string(b),
	}})
	return string(b)
}

// waitDiags waits for diagnostics of rel at version (0: any) and returns
// them as "line:col code" strings.
func (c *client) waitDiags(rel string, version int) []string {
	c.t.Helper()
	uri := c.uri(rel)
	deadline := time.After(5 * time.Second)
	for {
		c.mu.Lock()
		list := c.diags[uri]
		c.mu.Unlock()
		if n := len(list); n > 0 {
			last := list[n-1]
			if version == 0 || (last.Version != nil && *last.Version == version) {
				out := []string{}
				for _, d := range last.Diagnostics {
					out = append(out, fmt.Sprintf("%d:%d %s", d.Range.Start.Line, d.Range.Start.Character, d.Code))
				}
				return out
			}
		}
		select {
		case <-c.diagCh:
		case <-deadline:
			c.t.Fatalf("no diagnostics for %s v%d", rel, version)
		}
	}
}

// pos returns the position of the n-th occurrence (from 0) of needle in
// text, plus delta characters.
func pos(text, needle string, delta int) protocol.Position {
	i := strings.Index(text, needle)
	if i < 0 {
		panic("needle not found: " + needle)
	}
	i += delta
	return newMapper(text, protocol.EncodingUTF16).position(i)
}

func tdp(uri protocol.DocumentURI, p protocol.Position) map[string]any {
	return map[string]any{"textDocument": map[string]any{"uri": uri}, "position": p}
}

// ---------------------------------------------------------------------------

func TestLifecycle(t *testing.T) {
	srv := NewServer("test", nil)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- srv.Run(context.Background(), inR, outW) }()
	r := bufio.NewReader(outR)
	send := func(s string) { fmt.Fprintf(inW, "Content-Length: %d\r\n\r\n%s", len(s), s) }
	read := func() string {
		n := 0
		for {
			line, _ := r.ReadString('\n')
			if strings.TrimSpace(line) == "" {
				break
			}
			fmt.Sscanf(strings.TrimSpace(line), "Content-Length: %d", &n)
		}
		b := make([]byte, n)
		io.ReadFull(r, b)
		return string(b)
	}
	send(`{"jsonrpc":"2.0","id":1,"method":"textDocument/hover","params":{}}`)
	if got := read(); !strings.Contains(got, `"code":-32002`) {
		t.Errorf("before initialize: %s", got)
	}
	send(fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"rootUri":%q,"capabilities":{"general":{"positionEncodings":["utf-8","utf-16"]}},"initializationOptions":{"misc":{"scanOnStartup":false}}}}`, pathToURI(t.TempDir())))
	got := read()
	for _, want := range []string{`"positionEncoding":"utf-8"`, `"hoverProvider":true`, `"renameProvider":{"prepareProvider":true}`, `"name":"bem-ls"`} {
		if !strings.Contains(got, want) {
			t.Errorf("initialize result lacks %s: %s", want, got)
		}
	}
	send(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`)
	if got := read(); !strings.Contains(got, `"result":null`) {
		t.Errorf("shutdown: %s", got)
	}
	send(`{"jsonrpc":"2.0","method":"exit"}`)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run after shutdown+exit = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not exit")
	}
}

func TestExitWithoutShutdown(t *testing.T) {
	c := newClient(t, nil)
	c.notify("exit", nil)
	select {
	case err := <-c.done:
		if err == nil {
			t.Error("exit without shutdown should be an error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not exit")
	}
}

func TestDiagnosticsFollowEdits(t *testing.T) {
	c := newClient(t, nil)
	text := c.open("src/Card/Card.tsx")
	got := c.waitDiags("src/Card/Card.tsx", 1)
	want := []string{"5:18 missingInStylesheet", "5:32 invalidName", "5:32 missingInStylesheet"}
	if strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Errorf("v1 diagnostics %q, want %q", got, want)
	}

	// Fix the bad name in the component (incremental change).
	start := pos(text, "card__Bad", 0)
	end := pos(text, "card__Bad", len("card__Bad"))
	c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": c.uri("src/Card/Card.tsx"), "version": 2},
		"contentChanges": []any{map[string]any{"range": protocol.Range{Start: start, End: end}, "text": "card__ok"}},
	})
	if got := c.waitDiags("src/Card/Card.tsx", 2); strings.Join(got, ", ") != "5:18 missingInStylesheet, 5:32 missingInStylesheet" {
		t.Errorf("v2 diagnostics %q", got)
	}

	// Add the selectors to the open stylesheet: the component is re-checked.
	css := c.open("src/Card/Card.scss")
	c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": c.uri("src/Card/Card.scss"), "version": 2},
		"contentChanges": []any{map[string]any{"text": strings.Replace(css, "&__title {", "&__missing {} &__ok {} &__title {", 1)}},
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := c.waitDiags("src/Card/Card.tsx", 2); len(got) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("component diagnostics not cleared after stylesheet change")
		}
		<-c.diagCh
	}

	c.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": c.uri("src/Card/Card.tsx")}})
	if got := c.waitDiags("src/Card/Card.tsx", 0); len(got) != 0 {
		t.Errorf("after close: %q", got)
	}
}

func TestHover(t *testing.T) {
	c := newClient(t, nil)
	text := c.open("src/Card/Card.tsx")
	var h protocol.Hover
	c.call("textDocument/hover", tdp(c.uri("src/Card/Card.tsx"), pos(text, "card__title", 3)), &h)
	for _, want := range []string{"**`.card__title`** · static", "block `card` · element `title`", "Defined in `src/Card/Card.scss:4` as `&__title` → `.card__title`"} {
		if !strings.Contains(h.Contents.Value, want) {
			t.Errorf("hover lacks %q:\n%s", want, h.Contents.Value)
		}
	}
	c.call("textDocument/hover", tdp(c.uri("src/Card/Card.tsx"), pos(text, "<Title", 2)), &h)
	if !strings.Contains(h.Contents.Value, "styled.h2") || !strings.Contains(h.Contents.Value, "margin: 0;") {
		t.Errorf("styled hover:\n%s", h.Contents.Value)
	}

	css := c.open("src/Card/Card.scss")
	c.call("textDocument/hover", tdp(c.uri("src/Card/Card.scss"), pos(css, "&__title", 2)), &h)
	if !strings.Contains(h.Contents.Value, "Used 1 time(s): `src/Card/Card.tsx:5`") {
		t.Errorf("stylesheet hover:\n%s", h.Contents.Value)
	}
}

func TestCompletion(t *testing.T) {
	c := newClient(t, nil)
	text := c.open("src/Card/Card.tsx")
	var list protocol.CompletionList
	// Cursor after "card__" inside className="card__missing".
	c.call("textDocument/completion", tdp(c.uri("src/Card/Card.tsx"), pos(text, "card__missing", 6)), &list)
	var labels []string
	for _, it := range list.Items {
		labels = append(labels, it.SortText[:1]+":"+it.Label)
	}
	for _, it := range list.Items {
		if it.Label == "card__title" {
			r := it.TextEdit.Range
			if r.Start != pos(text, "card__missing", 0) || r.End != pos(text, "card__missing", len("card__missing")) {
				t.Errorf("edit range %+v", r)
			}
		}
	}
	sort.Strings(labels)
	// card__Bad is already in this attribute, so it isn't offered.
	if got := strings.Join(labels, " "); got != "1:card__title" {
		t.Errorf("class completions %q", got)
	}

	// From an empty prefix: the element prefix of the nearest block first,
	// then the stylesheet's classes, then classes used elsewhere in the file.
	c.call("textDocument/completion", tdp(c.uri("src/Card/Card.tsx"), pos(text, `"card__title"`, 1)), &list)
	labels = nil
	for _, it := range list.Items {
		labels = append(labels, it.SortText[:1]+":"+it.Label)
	}
	sort.Strings(labels)
	if got := strings.Join(labels, " "); got != "0:card__ 1:card 1:card__title 3:card__Bad 3:card__missing" {
		t.Errorf("class completions from empty prefix %q", got)
	}

	// Module members: styles.| offers both classes; the hyphenated one
	// switches to bracket access.
	ptext := c.open("src/Profile/Profile.tsx")
	c.call("textDocument/completion", tdp(c.uri("src/Profile/Profile.tsx"), pos(ptext, "s.avatar", 2)), &list)
	got := map[string]string{}
	for _, it := range list.Items {
		got[it.Label] = it.TextEdit.NewText
	}
	if got["avatar"] != "avatar" || got["stats-row"] != "['stats-row']" {
		t.Errorf("module completions %v", got)
	}

	// Elsewhere: nothing.
	c.call("textDocument/completion", tdp(c.uri("src/Card/Card.tsx"), pos(text, "export", 2)), &list)
	if len(list.Items) != 0 {
		t.Errorf("completions outside class strings: %v", list.Items)
	}
}

func TestDefinitionAndReferences(t *testing.T) {
	c := newClient(t, nil)
	text := c.open("src/Card/Card.tsx")
	var locs []protocol.Location
	c.call("textDocument/definition", tdp(c.uri("src/Card/Card.tsx"), pos(text, "card__title", 1)), &locs)
	if len(locs) != 1 || locs[0].URI != c.uri("src/Card/Card.scss") || locs[0].Range.Start.Line != 3 {
		t.Errorf("class definition %+v", locs)
	}
	c.call("textDocument/definition", tdp(c.uri("src/Card/Card.tsx"), pos(text, "<Title", 2)), &locs)
	if len(locs) != 1 || locs[0].URI != c.uri("src/Card/Card.styles.ts") || locs[0].Range.Start.Line != 1 {
		t.Errorf("styled definition %+v", locs)
	}
	c.call("textDocument/definition", tdp(c.uri("src/Card/Card.tsx"), pos(text, "./Card.scss", 3)), &locs)
	if len(locs) != 1 || locs[0].URI != c.uri("src/Card/Card.scss") {
		t.Errorf("import definition %+v", locs)
	}
	ptext := c.open("src/Profile/Profile.tsx")
	c.call("textDocument/definition", tdp(c.uri("src/Profile/Profile.tsx"), pos(ptext, "statsRow", 1)), &locs)
	if len(locs) != 1 || locs[0].URI != c.uri("src/Profile/Profile.module.scss") || locs[0].Range.Start.Line != 1 {
		t.Errorf("module definition %+v", locs)
	}

	css := c.open("src/Card/Card.scss")
	c.call("textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": c.uri("src/Card/Card.scss")},
		"position":     pos(css, "&__title", 3),
		"context":      map[string]any{"includeDeclaration": true},
	}, &locs)
	var got []string
	for _, l := range locs {
		got = append(got, fmt.Sprintf("%s:%d", filepath.Base(string(l.URI)), l.Range.Start.Line))
	}
	sort.Strings(got)
	if strings.Join(got, " ") != "Card.scss:3 Card.tsx:4" {
		t.Errorf("references %q", got)
	}

	styles := c.open("src/Card/Card.styles.ts")
	c.call("textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": c.uri("src/Card/Card.styles.ts")},
		"position":     pos(styles, "Title", 1),
		"context":      map[string]any{"includeDeclaration": false},
	}, &locs)
	if len(locs) != 1 || locs[0].URI != c.uri("src/Card/Card.tsx") {
		t.Errorf("styled references %+v", locs)
	}
}

// applyEdit applies a WorkspaceEdit's Changes to files on disk (for
// checking results).
func (c *client) applyEdit(e protocol.WorkspaceEdit) map[string]string {
	out := map[string]string{}
	for uri, edits := range e.Changes {
		path, _ := uriToPath(uri)
		b, _ := os.ReadFile(path)
		text := string(b)
		m := newMapper(text, protocol.EncodingUTF16)
		sort.Slice(edits, func(i, j int) bool { return m.offset(edits[i].Range.Start) > m.offset(edits[j].Range.Start) })
		for _, ed := range edits {
			text = text[:m.offset(ed.Range.Start)] + ed.NewText + text[m.offset(ed.Range.End):]
		}
		rel, _ := filepath.Rel(c.root, path)
		out[filepath.ToSlash(rel)] = text
	}
	return out
}

func TestRename(t *testing.T) {
	c := newClient(t, nil)
	css := c.open("src/Card/Card.scss")

	var prep protocol.PrepareRenameResult
	c.call("textDocument/prepareRename", tdp(c.uri("src/Card/Card.scss"), pos(css, "&__title", 2)), &prep)
	if prep.Placeholder != "card__title" {
		t.Errorf("prepareRename %+v", prep)
	}

	// Renaming the block renames the derived element too, in both files.
	var edit protocol.WorkspaceEdit
	c.call("textDocument/rename", map[string]any{
		"textDocument": map[string]any{"uri": c.uri("src/Card/Card.scss")},
		"position":     pos(css, ".card", 2),
		"newName":      "panel",
	}, &edit)
	files := c.applyEdit(edit)
	if !strings.HasPrefix(files["src/Card/Card.scss"], ".panel {") || !strings.Contains(files["src/Card/Card.scss"], "&__title {") {
		t.Errorf("Card.scss after rename:\n%s", files["src/Card/Card.scss"])
	}
	tsx := files["src/Card/Card.tsx"]
	if !strings.Contains(tsx, `className="panel"`) || !strings.Contains(tsx, `className="panel__title"`) ||
		!strings.Contains(tsx, `"card__missing card__Bad"`) {
		t.Errorf("Card.tsx after rename:\n%s", tsx)
	}

	// An element written &__title must keep its parent's prefix.
	r := c.request("textDocument/rename", map[string]any{
		"textDocument": map[string]any{"uri": c.uri("src/Card/Card.scss")},
		"position":     pos(css, "&__title", 3),
		"newName":      "heading",
	})
	if r.err == nil || !strings.Contains(r.err.Message, `must start with "card"`) {
		t.Errorf("rename to heading: %+v", r.err)
	}

	// From a CSS-module alias: styles.statsRow → .stats-row.
	ptext := c.open("src/Profile/Profile.tsx")
	c.call("textDocument/rename", map[string]any{
		"textDocument": map[string]any{"uri": c.uri("src/Profile/Profile.tsx")},
		"position":     pos(ptext, "statsRow", 2),
		"newName":      "statsColumn",
	}, &edit)
	files = c.applyEdit(edit)
	if !strings.Contains(files["src/Profile/Profile.module.scss"], ".stats-column {}") ||
		!strings.Contains(files["src/Profile/Profile.tsx"], "s.statsColumn") {
		t.Errorf("module rename: %v", files)
	}

	r = c.request("textDocument/rename", map[string]any{
		"textDocument": map[string]any{"uri": c.uri("src/Card/Card.scss")},
		"position":     pos(css, ".card", 2),
		"newName":      "not valid",
	})
	if r.err == nil {
		t.Error("invalid new name accepted")
	}
}

func TestCodeActions(t *testing.T) {
	c := newClient(t, nil)
	text := c.open("src/Card/Card.tsx")
	var actions []protocol.CodeAction
	at := pos(text, "card__Bad", 1)
	c.call("textDocument/codeAction", map[string]any{
		"textDocument": map[string]any{"uri": c.uri("src/Card/Card.tsx")},
		"range":        protocol.Range{Start: at, End: at},
		"context":      map[string]any{"diagnostics": []any{}},
	}, &actions)
	titles := map[string]protocol.CodeAction{}
	for _, a := range actions {
		titles[a.Title] = a
	}
	fix, ok := titles[`Change to "card__bad"`]
	if !ok {
		t.Fatalf("no rename quick fix in %v", keys(titles))
	}
	if e := fix.Edit.Changes[c.uri("src/Card/Card.tsx")]; len(e) != 1 || e[0].NewText != "card__bad" {
		t.Errorf("fix edit %+v", fix.Edit)
	}
	add, ok := titles["Add .card__Bad to Card.scss"]
	if !ok {
		t.Fatalf("no add-selector action in %v", keys(titles))
	}
	got := c.applyEdit(*add.Edit)["src/Card/Card.scss"]
	if !strings.Contains(got, "  }\n  &__Bad {}\n}") {
		t.Errorf("Card.scss after add:\n%s", got)
	}
	all, ok := titles["Add all 2 missing selectors to Card.scss"]
	if !ok {
		t.Fatalf("no add-all action in %v", keys(titles))
	}
	got = c.applyEdit(*all.Edit)["src/Card/Card.scss"]
	if !strings.Contains(got, "&__missing {}") || !strings.Contains(got, "&__Bad {}") {
		t.Errorf("Card.scss after add all:\n%s", got)
	}

	// "only" filters by kind.
	c.call("textDocument/codeAction", map[string]any{
		"textDocument": map[string]any{"uri": c.uri("src/Card/Card.tsx")},
		"range":        protocol.Range{Start: at, End: at},
		"context":      map[string]any{"diagnostics": []any{}, "only": []string{"source"}},
	}, &actions)
	for _, a := range actions {
		if a.Kind != "source" {
			t.Errorf("only=source returned %s %q", a.Kind, a.Title)
		}
	}

	// A component without a stylesheet can create one.
	ltext := c.open("src/Lonely/Lonely.tsx")
	start := pos(ltext, "export", 0)
	var raw []map[string]any
	c.call("textDocument/codeAction", map[string]any{
		"textDocument": map[string]any{"uri": c.uri("src/Lonely/Lonely.tsx")},
		"range":        protocol.Range{Start: start, End: start},
		"context":      map[string]any{"diagnostics": []any{}},
	}, &raw)
	if len(raw) != 1 || raw[0]["title"] != "Create Lonely.scss with the BEM skeleton" {
		t.Fatalf("create action: %v", raw)
	}
	b, _ := json.Marshal(raw[0]["edit"])
	for _, want := range []string{`"kind":"create"`, `Lonely.scss"`, `".lonely {\n  \u0026__text {}\n}\n"`, `"import './Lonely.scss';\n"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("create edit lacks %s: %s", want, b)
		}
	}
}

func keys(m map[string]protocol.CodeAction) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestExecuteGenerateSkeleton(t *testing.T) {
	c := newClient(t, nil)
	var out string
	c.call("workspace/executeCommand", map[string]any{
		"command": CmdGenerateSkeleton, "arguments": []any{c.uri("src/Card/Card.tsx")},
	}, &out)
	if out != ".card {\n  &__title {}\n  &__missing {}\n  &__Bad {}\n}\n" {
		t.Errorf("skeleton %q", out)
	}
	r := c.request("workspace/executeCommand", map[string]any{"command": "bem.nope"})
	if r.err == nil {
		t.Error("unknown command accepted")
	}
}

func TestConfigurationChange(t *testing.T) {
	c := newClient(t, map[string]any{"rules": map[string]string{"invalidName": "off"}})
	c.open("src/Card/Card.tsx")
	if got := c.waitDiags("src/Card/Card.tsx", 1); strings.Contains(strings.Join(got, " "), "invalidName") {
		t.Errorf("invalidName is off: %q", got)
	}
	c.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"bem": map[string]any{
		"rules": map[string]string{"missingInStylesheet": "off"},
		"misc":  map[string]any{"diagnosticsDelay": 5},
	}}})
	deadline := time.After(5 * time.Second)
	for {
		got := strings.Join(c.waitDiags("src/Card/Card.tsx", 1), " ")
		if strings.Contains(got, "invalidName") && !strings.Contains(got, "missingInStylesheet") {
			break
		}
		select {
		case <-c.diagCh:
		case <-deadline:
			t.Fatalf("configuration not applied: %q", got)
		}
	}
}

func TestInvalidInitializationOptions(t *testing.T) {
	c := newClient(t, map[string]any{"naming": map[string]string{"preset": "bootstrap"}})
	time.Sleep(100 * time.Millisecond)
	c.mu.Lock()
	msgs := strings.Join(c.msgs, "\n")
	c.mu.Unlock()
	if !strings.Contains(msgs, `unknown naming preset \"bootstrap\"`) {
		t.Errorf("no warning shown: %s", msgs)
	}
	// The server still works with defaults.
	c.open("src/Card/Card.tsx")
	if got := c.waitDiags("src/Card/Card.tsx", 1); len(got) == 0 {
		t.Error("no diagnostics with default settings")
	}
}

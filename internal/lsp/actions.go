package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"scss-bem-gen/internal/bem"
	"scss-bem-gen/internal/core"
	"scss-bem-gen/internal/generator"
	"scss-bem-gen/internal/index"
	"scss-bem-gen/internal/lsp/jsonrpc"
	"scss-bem-gen/internal/lsp/protocol"
	"scss-bem-gen/internal/scss"
)

// ---------------------------------------------------------------------------
// Rename

func renameError(format string, args ...any) error {
	return jsonrpc.Errorf(jsonrpc.CodeRequestFailed, format, args...)
}

func (s *Server) prepareRename(ctx context.Context, p protocol.TextDocumentPositionParams) (*protocol.PrepareRenameResult, error) {
	d, m, off, err := s.docAt(p.TextDocument.URI, p.Position)
	if err != nil {
		return nil, nil
	}
	ix, _, _ := s.state()
	switch d.Kind {
	case index.KindJSX:
		if sh := d.SheetAt(off); sh != nil {
			return s.prepareRenameSymbol(ix, d, sh, m, off)
		}
		if _, ref, ok := classAt(d.JSX.Root, off); ok {
			if ref.Kind == core.ClassDynamic {
				return nil, renameError("a dynamic class name can't be renamed")
			}
			return &protocol.PrepareRenameResult{Range: m.rangeOf(ref.Range), Placeholder: ref.Name}, nil
		}
	case index.KindStylesheet:
		if d.Sheet != nil {
			if c, ok := classDefAt(d.Sheet, off); ok {
				return &protocol.PrepareRenameResult{Range: m.rangeOf(c.Range), Placeholder: c.Name}, nil
			}
			return s.prepareRenameSymbol(ix, d, d.Sheet, m, off)
		}
	}
	return nil, nil
}

func (s *Server) rename(ctx context.Context, p protocol.RenameParams) (*protocol.WorkspaceEdit, error) {
	d, _, off, err := s.docAt(p.TextDocument.URI, p.Position)
	if err != nil {
		return nil, err
	}
	ix, _, enc := s.state()
	newName := strings.TrimSpace(p.NewName)

	var (
		cssOld, cssNew string
		sheets         []string
	)
	switch d.Kind {
	case index.KindJSX:
		if sh := d.SheetAt(off); sh != nil {
			return s.renameSymbol(ix, enc, d, sh, off, newName)
		}
		_, ref, ok := classAt(d.JSX.Root, off)
		if !ok {
			return nil, renameError("no class at this position")
		}
		if ref.Kind == core.ClassDynamic {
			return nil, renameError("a dynamic class name can't be renamed")
		}
		defs := ix.ClassDefinitions(d.Path, ref)
		if len(defs) == 0 {
			// Not in any stylesheet: rename within this file.
			if !validClassName(newName) {
				return nil, renameError("%q is not a valid class name", newName)
			}
			return s.renameInFile(d, ref, newName, enc), nil
		}
		cssOld = defName(ix, defs[0])
		cssNew = newName
		if ref.Kind == core.ClassModule && ref.Name != cssOld {
			// styles.statsRow aliases .stats-row: rename .stats-row to the
			// kebab-case form of the new identifier.
			cssNew = kebabCase(newName)
		}
		for _, def := range defs {
			if !containsStr(sheets, def.Path) {
				sheets = append(sheets, def.Path)
			}
		}
	case index.KindStylesheet:
		if d.Sheet == nil {
			return nil, renameError("this stylesheet can't be parsed")
		}
		c, ok := classDefAt(d.Sheet, off)
		if !ok {
			return s.renameSymbol(ix, enc, d, d.Sheet, off, newName)
		}
		cssOld, cssNew, sheets = c.Name, newName, []string{d.Path}
	default:
		return nil, renameError("nothing to rename")
	}
	if !validClassName(cssNew) {
		return nil, renameError("%q is not a valid class name", cssNew)
	}
	if cssNew == cssOld {
		return &protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{}}, nil
	}

	edits := map[protocol.DocumentURI][]protocol.TextEdit{}
	renamed := map[string]string{} // every class whose name changes
	for _, path := range sheets {
		sd, err := ix.Get(path)
		if err != nil || sd.Sheet == nil {
			continue
		}
		newText, err := renameInSheet(sd.Text, sd.Sheet, cssOld, cssNew)
		if err != nil {
			return nil, renameError("%s: %v", relPath(ix.Root(), path), err)
		}
		// Re-parse to find classes that change as a side effect: renaming
		// .card also renames &__title to newname__title.
		after := scss.Parse(path, newText)
		if len(after.Classes) != len(sd.Sheet.Classes) {
			return nil, renameError("%s: renaming would change the stylesheet's structure", relPath(ix.Root(), path))
		}
		for i, c := range sd.Sheet.Classes {
			if n := after.Classes[i].Name; n != c.Name {
				renamed[c.Name] = n
			}
		}
		edits[pathToURI(path)] = append(edits[pathToURI(path)], diffEdit(newMapper(sd.Text, enc), sd.Text, newText))
	}

	// Rename the uses in components.
	seen := map[string]bool{}
	olds := make([]string, 0, len(renamed))
	for o := range renamed {
		olds = append(olds, o)
	}
	sort.Strings(olds)
	for _, o := range olds {
		n := renamed[o]
		for _, path := range sheets {
			for _, u := range ix.ClassUsages(path, o) {
				key := fmt.Sprintf("%s:%d", u.Path, u.Range.Start.Offset)
				if seen[key] {
					continue
				}
				seen[key] = true
				ud, err := ix.Get(u.Path)
				if err != nil || ud.JSX == nil {
					continue
				}
				_, ref, ok := classAt(ud.JSX.Root, u.Range.Start.Offset)
				if !ok {
					continue
				}
				text, err := jsxReplacement(ud.Text, ref, o, n)
				if err != nil {
					return nil, renameError("%s:%d: %v", relPath(ix.Root(), u.Path), u.Range.Start.Line, err)
				}
				uri := pathToURI(u.Path)
				edits[uri] = append(edits[uri], protocol.TextEdit{
					Range: newMapper(ud.Text, enc).rangeOf(ref.Range), NewText: text,
				})
			}
		}
	}
	return &protocol.WorkspaceEdit{Changes: edits}, nil
}

// defName returns the class name a definition location defines.
func defName(ix *index.Index, def index.Location) string {
	if sd, err := ix.Get(def.Path); err == nil && sd.Sheet != nil {
		for _, c := range sd.Sheet.Classes {
			if c.Range == def.Range {
				return c.Name
			}
		}
	}
	return ""
}

// renameInSheet renames every definition of old in a stylesheet. A
// definition written "&suffix" can only change its suffix, so the new name
// must keep the parent's part.
func renameInSheet(text string, sheet *scss.Sheet, old, new string) (string, error) {
	type edit struct {
		start, end int
		text       string
	}
	var edits []edit
	done := map[int]bool{}
	for _, c := range sheet.ClassDefs(old) {
		start, end := c.Range.Start.Offset, c.Range.End.Offset
		if done[start] {
			continue
		}
		done[start] = true
		src := text[start:end]
		switch {
		case src == "."+old:
			edits = append(edits, edit{start + 1, end, new})
		case strings.HasPrefix(src, "&"):
			for _, other := range sheet.Classes {
				if other.Range == c.Range && other.Name != old {
					return "", fmt.Errorf("`%s` also defines .%s; rename the parent selectors instead", src, other.Name)
				}
			}
			suffix := src[1:]
			parent := strings.TrimSuffix(old, suffix)
			if !strings.HasPrefix(new, parent) || len(new) == len(parent) {
				return "", fmt.Errorf(".%s comes from `%s` inside .%s, so the new name must start with %q", old, src, parent, parent)
			}
			edits = append(edits, edit{start + 1, end, new[len(parent):]})
		default:
			return "", fmt.Errorf("can't rename `%s`", src)
		}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	for _, e := range edits {
		text = text[:e.start] + e.text + text[e.end:]
	}
	return text, nil
}

// jsxReplacement returns the new text for a class reference when class old
// becomes new.
func jsxReplacement(src string, ref core.ClassRef, old, new string) (string, error) {
	if ref.Kind != core.ClassModule {
		return new, nil
	}
	start := ref.Range.Start.Offset
	bracket := start > 0 && (src[start-1] == '\'' || src[start-1] == '"')
	name := new
	if ref.Name != old {
		name = camelCase(new) // styles.statsRow for .stats-row
	}
	if !bracket && !isJSIdent(name) {
		return "", fmt.Errorf("%s.%s would not be valid JavaScript; use %s['%s']", ref.Module, name, ref.Module, name)
	}
	return name, nil
}

func (s *Server) renameInFile(d *index.Doc, ref core.ClassRef, newName, enc string) *protocol.WorkspaceEdit {
	m := newMapper(d.Text, enc)
	var edits []protocol.TextEdit
	walkRefs(d.JSX.Root, func(c core.ClassRef) {
		if c.Name == ref.Name && c.Kind != core.ClassDynamic && (c.Kind == core.ClassModule) == (ref.Kind == core.ClassModule) && c.Module == ref.Module {
			edits = append(edits, protocol.TextEdit{Range: m.rangeOf(c.Range), NewText: newName})
		}
	})
	return &protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{pathToURI(d.Path): edits}}
}

func validClassName(s string) bool {
	t := strings.TrimPrefix(s, "-")
	if t == "" {
		return false
	}
	for i, r := range t {
		ok := r == '_' || r > 127 || unicode.IsLetter(r)
		if i > 0 {
			ok = ok || r == '-' || unicode.IsDigit(r)
		}
		if !ok {
			return false
		}
	}
	return true
}

func kebabCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('-')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

func camelCase(s string) string {
	parts := strings.Split(s, "-")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

// ---------------------------------------------------------------------------
// Code actions

func (s *Server) codeAction(ctx context.Context, p protocol.CodeActionParams) ([]protocol.CodeAction, error) {
	d, m, _, err := s.docAt(p.TextDocument.URI, p.Range.Start)
	if err != nil {
		return nil, nil
	}
	var actions []protocol.CodeAction
	switch d.Kind {
	case index.KindJSX:
		actions = s.jsxActions(d, m, p)
	case index.KindStylesheet:
		actions = s.stylesheetActions(d, m, p)
	}
	if len(p.Context.Only) > 0 {
		var kept []protocol.CodeAction
		for _, a := range actions {
			for _, k := range p.Context.Only {
				if a.Kind == k || strings.HasPrefix(a.Kind, k+".") {
					kept = append(kept, a)
					break
				}
			}
		}
		actions = kept
	}
	if actions == nil {
		actions = []protocol.CodeAction{}
	}
	return actions, nil
}

func overlaps(a, b protocol.Range) bool {
	before := func(x, y protocol.Position) bool {
		return x.Line < y.Line || (x.Line == y.Line && x.Character < y.Character)
	}
	return !before(a.End, b.Start) && !before(b.End, a.Start)
}

func (s *Server) jsxActions(d *index.Doc, m *mapper, p protocol.CodeActionParams) []protocol.CodeAction {
	ix, cfg, enc := s.state()
	in, err := ix.LintInput(d.Path)
	if err != nil {
		return nil
	}
	diags := cfg.linter.Lint(in)
	uri := pathToURI(d.Path)
	primary := ix.PrimaryStylesheets(d.Path)

	var actions []protocol.CodeAction
	var missing []string
	for _, f := range diags {
		if f.Rule == bem.RuleMissingInStylesheet && !containsStr(missing, f.Class) {
			missing = append(missing, f.Class)
		}
	}
	for _, f := range diags {
		r := m.rangeOf(f.Range)
		if !overlaps(r, p.Range) {
			continue
		}
		pd := protocol.Diagnostic{Range: r, Severity: int(f.Severity), Code: string(f.Rule), Source: "bem", Message: f.Message}
		if f.Suggestion != "" {
			actions = append(actions, protocol.CodeAction{
				Title:       fmt.Sprintf("Change to %q", f.Suggestion),
				Kind:        protocol.CodeActionQuickFix,
				Diagnostics: []protocol.Diagnostic{pd},
				IsPreferred: f.Rule == bem.RuleInvalidName,
				Edit: &protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{
					uri: {{Range: r, NewText: f.Suggestion}},
				}},
			})
		}
		if f.Rule == bem.RuleMissingInStylesheet && len(primary) > 0 {
			if edit := s.addSelectorsEdit(ix, cfg, enc, primary[0], []string{f.Class}); edit != nil {
				actions = append(actions, protocol.CodeAction{
					Title:       fmt.Sprintf("Add .%s to %s", f.Class, filepath.Base(primary[0])),
					Kind:        protocol.CodeActionQuickFix,
					Diagnostics: []protocol.Diagnostic{pd},
					IsPreferred: true,
					Edit:        edit,
				})
			}
		}
	}

	if len(missing) > 1 && len(primary) > 0 {
		if edit := s.addSelectorsEdit(ix, cfg, enc, primary[0], missing); edit != nil {
			actions = append(actions, protocol.CodeAction{
				Title: fmt.Sprintf("Add all %d missing selectors to %s", len(missing), filepath.Base(primary[0])),
				Kind:  protocol.CodeActionSource,
				Edit:  edit,
			})
		}
	}
	if a := s.createStylesheetAction(ix, cfg, d); a != nil {
		actions = append(actions, *a)
	}
	return actions
}

func (s *Server) addSelectorsEdit(ix *index.Index, cfg *compiled, enc, sheetPath string, classes []string) *protocol.WorkspaceEdit {
	sd, err := ix.Get(sheetPath)
	if err != nil || sd.Sheet == nil {
		return nil
	}
	newText := addSelectors(sd.Text, classes, cfg.linter.Naming())
	if newText == sd.Text {
		return nil
	}
	return &protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{
		pathToURI(sheetPath): {diffEdit(newMapper(sd.Text, enc), sd.Text, newText)},
	}}
}

// createStylesheetAction offers to create Component.scss with the BEM
// skeleton and import it, when a component has no stylesheet.
func (s *Server) createStylesheetAction(ix *index.Index, cfg *compiled, d *index.Doc) *protocol.CodeAction {
	s.mu.Lock()
	can := s.caps.documentChanges && s.caps.createFiles
	var version *int
	if od, ok := s.open[d.Path]; ok {
		v := od.version
		version = &v
	}
	s.mu.Unlock()
	if !can || len(ix.PrimaryStylesheets(d.Path)) > 0 || len(d.JSX.Stylesheets()) > 0 {
		return nil
	}
	content := generator.Generate(d.JSX.Root, cfg.genOpts)
	if content == "" {
		return nil
	}
	ext := ".scss"
	for _, e := range ix.Config().Extensions {
		if !strings.Contains(e, ".module.") && e != ".sass" {
			ext = e
			break
		}
	}
	base := strings.TrimSuffix(filepath.Base(d.Path), filepath.Ext(d.Path))
	target := filepath.Join(filepath.Dir(d.Path), base+ext)
	if _, err := os.Stat(target); err == nil {
		return nil
	}

	// Import after the last import, or at the top.
	_, _, enc := s.state()
	m := newMapper(d.Text, enc)
	at := 0
	for _, imp := range d.JSX.Imports {
		if !imp.Require && imp.Range.End.Offset > at {
			at = imp.Range.End.Offset
		}
	}
	importLine := fmt.Sprintf("import './%s';\n", filepath.Base(target))
	if at > 0 {
		if nl := strings.IndexByte(d.Text[at:], '\n'); nl >= 0 {
			at += nl + 1
		} else {
			at = len(d.Text)
			importLine = "\n" + importLine
		}
	}
	pos := m.position(at)
	zero := protocol.Range{}
	targetURI := pathToURI(target)
	return &protocol.CodeAction{
		Title: fmt.Sprintf("Create %s with the BEM skeleton", filepath.Base(target)),
		Kind:  protocol.CodeActionSource,
		Edit: &protocol.WorkspaceEdit{DocumentChanges: []any{
			protocol.CreateFile{Kind: "create", URI: targetURI, Options: &protocol.CreateFileOptions{IgnoreIfExists: true}},
			protocol.TextDocumentEdit{
				TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{URI: targetURI},
				Edits:        []protocol.TextEdit{{Range: zero, NewText: content}},
			},
			protocol.TextDocumentEdit{
				TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{URI: pathToURI(d.Path), Version: version},
				Edits:        []protocol.TextEdit{{Range: protocol.Range{Start: pos, End: pos}, NewText: importLine}},
			},
		}},
	}
}

// ---------------------------------------------------------------------------
// Commands

func (s *Server) executeCommand(ctx context.Context, p protocol.ExecuteCommandParams) (any, error) {
	switch p.Command {
	case CmdGenerateSkeleton:
		if len(p.Arguments) == 0 {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "%s needs a document URI", p.Command)
		}
		var uri protocol.DocumentURI
		if err := json.Unmarshal(p.Arguments[0], &uri); err != nil {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "%s: %v", p.Command, err)
		}
		ix, cfg, _ := s.state()
		path, err := uriToPath(uri)
		if err != nil {
			return nil, err
		}
		d, err := ix.Get(path)
		if err != nil {
			return nil, err
		}
		if d.Kind != index.KindJSX {
			return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "%s is not a JSX/TSX file", filepath.Base(path))
		}
		return generator.Generate(d.JSX.Root, cfg.genOpts), nil
	}
	return nil, jsonrpc.Errorf(jsonrpc.CodeInvalidParams, "unknown command %q", p.Command)
}

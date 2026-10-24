package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/TeddyGavi/bem-ls/internal/lsp/protocol"
)

// stylesheetWorkspace adds files for phase 5 to the base test workspace.
var stylesheetWorkspace = map[string]string{
	"src/styles/_tokens.scss": `/// Brand colour.
$brand: #0af;
$primary: $brand;
@mixin focus($w: 2px) { outline: $w solid $primary; }
@function rem($px) { @return $px / 16px * 1rem; }
%reset { margin: 0; }
`,
	"src/styles/_index.scss": "@forward 'tokens';\n",
	"src/Card/Card.scss": `@use '../styles' as t;

.card {
  color: t.$primary;
  @include t.focus;
  padding: t.rem(8px);

  &__title {
    @extend %reset;
    color: t.$primary;
  }

  &__unused {}
}
`,
	"src/Card/Card.tsx": `import './Card.scss';
export const Card = () => (
  <div className="card">
    <h2 className="card__title card__title--big">Hi</h2>
    <div className="card__body"><p className="card__text" /></div>
    <span className="badge" />
  </div>
);
`,
	"src/Empty/Empty.tsx":     `export const Empty = () => <ul className="list"><li className="list__item" /></ul>;` + "\n",
	"src/Empty/Empty.scss":    "",
	"src/Mod/Mod.tsx":         `import s from './Mod.module.scss';` + "\nexport const Mod = () => <div className={clsx(s.root, s.isOpen, s.ok)} />;\n",
	"src/Mod/Mod.module.scss": ".ok {}\n",
	"src/Styled/Box.tsx": "import styled from 'styled-components';\n" +
		"const gap = 1;\nexport const Box = styled.div`\n  --box-gap: 4px;\n  margin: var(--box-gap);\n  $local: 1px;\n  padding: $local;\n`;\n",
}

func newStylesheetClient(t *testing.T) *client {
	t.Helper()
	saved := map[string]string{}
	for k, v := range stylesheetWorkspace {
		saved[k], testWorkspace[k] = testWorkspace[k], v
	}
	defer func() {
		for k, v := range saved {
			if v == "" {
				delete(testWorkspace, k)
			} else {
				testWorkspace[k] = v
			}
		}
	}()
	return newClient(t, nil)
}

func TestStylesheetDefinitionHoverReferences(t *testing.T) {
	c := newStylesheetClient(t)
	css := c.open("src/Card/Card.scss")
	uri := c.uri("src/Card/Card.scss")

	var locs []protocol.Location
	c.call("textDocument/definition", tdp(uri, pos(css, "t.$primary", 3)), &locs)
	if len(locs) != 1 || locs[0].URI != c.uri("src/styles/_tokens.scss") || locs[0].Range.Start.Line != 2 {
		t.Errorf("variable definition %+v", locs)
	}
	c.call("textDocument/definition", tdp(uri, pos(css, "t.focus", 3)), &locs)
	if len(locs) != 1 || locs[0].Range.Start.Line != 3 {
		t.Errorf("mixin definition %+v", locs)
	}
	c.call("textDocument/definition", tdp(uri, pos(css, "t.rem", 3)), &locs)
	if len(locs) != 1 || locs[0].Range.Start.Line != 4 {
		t.Errorf("function definition %+v", locs)
	}
	c.call("textDocument/definition", tdp(uri, pos(css, "%reset", 2)), &locs)
	if len(locs) != 1 || locs[0].Range.Start.Line != 5 {
		t.Errorf("placeholder definition %+v", locs)
	}
	// The namespace part goes to the module file; the URL too.
	c.call("textDocument/definition", tdp(uri, pos(css, "t.$primary", 0)), &locs)
	if len(locs) != 1 || locs[0].URI != c.uri("src/styles/_index.scss") {
		t.Errorf("namespace definition %+v", locs)
	}
	c.call("textDocument/definition", tdp(uri, pos(css, "../styles", 2)), &locs)
	if len(locs) != 1 || locs[0].URI != c.uri("src/styles/_index.scss") {
		t.Errorf("@use URL definition %+v", locs)
	}

	var h protocol.Hover
	c.call("textDocument/hover", tdp(uri, pos(css, "t.$primary", 4)), &h)
	for _, want := range []string{"$primary: $brand;", "Resolves to `#0af` (via `$brand`)", "Defined in `src/styles/_tokens.scss:3`"} {
		if !strings.Contains(h.Contents.Value, want) {
			t.Errorf("variable hover lacks %q:\n%s", want, h.Contents.Value)
		}
	}
	c.call("textDocument/hover", tdp(uri, pos(css, "t.focus", 3)), &h)
	if !strings.Contains(h.Contents.Value, "@mixin focus($w: 2px)") {
		t.Errorf("mixin hover:\n%s", h.Contents.Value)
	}

	tokens := c.open("src/styles/_tokens.scss")
	c.call("textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": c.uri("src/styles/_tokens.scss")},
		"position":     pos(tokens, "$primary:", 2),
		"context":      map[string]any{"includeDeclaration": true},
	}, &locs)
	var got []string
	for _, l := range locs {
		got = append(got, fmt.Sprintf("%s:%d", filepath.Base(string(l.URI)), l.Range.Start.Line))
	}
	sort.Strings(got)
	// Declaration, the use in the mixin, and two uses in Card.scss.
	if want := "Card.scss:3 Card.scss:9 _tokens.scss:2 _tokens.scss:3"; strings.Join(got, " ") != want {
		t.Errorf("references %q, want %q", strings.Join(got, " "), want)
	}

	var hl []protocol.DocumentHighlight
	c.call("textDocument/documentHighlight", tdp(uri, pos(css, "t.$primary", 4)), &hl)
	if len(hl) != 2 || hl[0].Kind != protocol.HighlightRead {
		t.Errorf("highlights %+v", hl)
	}
}

func TestStylesheetSymbolRename(t *testing.T) {
	c := newStylesheetClient(t)
	css := c.open("src/Card/Card.scss")
	uri := c.uri("src/Card/Card.scss")

	var prep protocol.PrepareRenameResult
	c.call("textDocument/prepareRename", tdp(uri, pos(css, "t.$primary", 4)), &prep)
	if prep.Placeholder != "primary" || prep.Range.Start != pos(css, "t.$primary", 3) {
		t.Errorf("prepareRename %+v", prep)
	}
	var edit protocol.WorkspaceEdit
	c.call("textDocument/rename", map[string]any{
		"textDocument": map[string]any{"uri": uri}, "position": pos(css, "t.$primary", 4), "newName": "$main",
	}, &edit)
	files := c.applyEdit(edit)
	if strings.Count(files["src/Card/Card.scss"], "t.$main") != 2 {
		t.Errorf("Card.scss after rename:\n%s", files["src/Card/Card.scss"])
	}
	if tok := files["src/styles/_tokens.scss"]; !strings.Contains(tok, "$main: $brand;") || !strings.Contains(tok, "solid $main;") {
		t.Errorf("_tokens.scss after rename:\n%s", tok)
	}
}

func TestStyledTemplateFeatures(t *testing.T) {
	c := newStylesheetClient(t)
	text := c.open("src/Styled/Box.tsx")
	uri := c.uri("src/Styled/Box.tsx")
	var locs []protocol.Location
	c.call("textDocument/definition", tdp(uri, pos(text, "var(--box-gap", 6)), &locs)
	if len(locs) != 1 || locs[0].Range.Start.Line != 3 {
		t.Errorf("custom property definition %+v", locs)
	}
	c.call("textDocument/definition", tdp(uri, pos(text, "padding: $local", 10)), &locs)
	if len(locs) != 1 || locs[0].Range.Start.Line != 5 {
		t.Errorf("variable definition in template %+v", locs)
	}
	var h protocol.Hover
	c.call("textDocument/hover", tdp(uri, pos(text, "var(--box-gap", 6)), &h)
	if !strings.Contains(h.Contents.Value, "--box-gap: 4px;") {
		t.Errorf("hover in template:\n%s", h.Contents.Value)
	}
	var folds []protocol.FoldingRange
	c.call("textDocument/foldingRange", map[string]any{"textDocument": map[string]any{"uri": uri}}, &folds)
	if len(folds) != 0 {
		t.Errorf("folds in a template without blocks: %+v", folds)
	}
}

func TestDocumentSymbolsAndFolding(t *testing.T) {
	c := newStylesheetClient(t)
	css := c.open("src/Card/Card.scss")
	uri := c.uri("src/Card/Card.scss")
	var syms []protocol.DocumentSymbol
	c.call("textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": uri}}, &syms)
	var render func(ss []protocol.DocumentSymbol, depth int) string
	render = func(ss []protocol.DocumentSymbol, depth int) string {
		var b strings.Builder
		for _, s := range ss {
			fmt.Fprintf(&b, "%s%s [%d] %s\n", strings.Repeat("  ", depth), s.Name, s.Kind, s.Detail)
			b.WriteString(render(s.Children, depth+1))
		}
		return b.String()
	}
	want := ".card [5] \n  &__title [5] .card__title\n  &__unused [5] .card__unused\n"
	if got := render(syms, 0); got != want {
		t.Errorf("outline:\n%s\nwant:\n%s", got, want)
	}

	c.open("src/styles/_tokens.scss")
	syms = nil // json.Unmarshal into a used slice keeps old Children
	c.call("textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": c.uri("src/styles/_tokens.scss")}}, &syms)
	if got := render(syms, 0); got != "$brand [13] #0af\n$primary [13] $brand\n@mixin focus($w: 2px) [6] \n@function rem($px) [12] \n%reset [11] \n" {
		t.Errorf("tokens outline:\n%s", got)
	}

	var folds []protocol.FoldingRange
	c.call("textDocument/foldingRange", map[string]any{"textDocument": map[string]any{"uri": uri}}, &folds)
	var fr []string
	for _, f := range folds {
		fr = append(fr, fmt.Sprintf("%d-%d", f.StartLine, f.EndLine))
	}
	// Folds end before the closing brace's line.
	if strings.Join(fr, " ") != "2-12 7-9" {
		t.Errorf("folding %q in\n%s", fr, css)
	}

	var ws []protocol.SymbolInformation
	c.call("workspace/symbol", map[string]any{"query": "prim"}, &ws)
	if len(ws) != 1 || ws[0].Name != "$primary" || ws[0].ContainerName != "src/styles/_tokens.scss" {
		t.Errorf("workspace symbols %+v", ws)
	}
	c.call("workspace/symbol", map[string]any{"query": ".crdttl"}, &ws)
	if len(ws) != 1 || ws[0].Name != ".card__title" {
		t.Errorf("fuzzy class symbols %+v", ws)
	}
}

func TestUnusedSelectorHints(t *testing.T) {
	c := newStylesheetClient(t)
	c.open("src/Card/Card.scss")
	got := c.waitDiags("src/Card/Card.scss", 1)
	if strings.Join(got, " ") != "12:2 unusedSelector" {
		t.Errorf("stylesheet diagnostics %q", got)
	}
	c.mu.Lock()
	last := c.diags[c.uri("src/Card/Card.scss")]
	c.mu.Unlock()
	d := last[len(last)-1].Diagnostics[0]
	if d.Severity != protocol.SeverityHint || len(d.Tags) != 1 || d.Tags[0] != protocol.TagUnnecessary {
		t.Errorf("unused hint %+v", d)
	}
}

// ghost asks for inline completions with the cursor at the "|" in text,
// after replacing the open document's content.
func (c *client) ghost(rel, text string, version int) (string, protocol.Range, bool) {
	c.t.Helper()
	off := strings.Index(text, "|")
	text = text[:off] + text[off+1:]
	c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": c.uri(rel), "version": version},
		"contentChanges": []any{map[string]any{"text": text}},
	})
	var list protocol.InlineCompletionList
	c.call("textDocument/inlineCompletion", map[string]any{
		"textDocument": map[string]any{"uri": c.uri(rel)},
		"position":     newMapper(text, protocol.EncodingUTF16).position(off),
		"context":      map[string]any{"triggerKind": 2},
	}, &list)
	if len(list.Items) == 0 {
		return "", protocol.Range{}, false
	}
	return list.Items[0].InsertText, *list.Items[0].Range, true
}

func TestGhostText(t *testing.T) {
	c := newStylesheetClient(t)
	c.open("src/Card/Card.scss")
	rel := "src/Card/Card.scss"

	// Inside .card on a blank, indented line: its missing children.
	got, r, ok := c.ghost(rel, ".card {\n  color: red;\n  |\n  &__title {}\n}\n", 2)
	if !ok || got != "  &__body {}\n  &__text {}" || r.Start.Character != 0 || r.End.Character != 2 {
		t.Errorf("inside .card: %q %+v", got, r)
	}
	// Inside &__title: its modifier.
	got, _, ok = c.ghost(rel, ".card {\n  &__title {\n    |\n  }\n}\n", 3)
	if !ok || got != "    &--big {}" {
		t.Errorf("inside &__title: %q", got)
	}
	// At the top level: missing blocks only.
	got, _, ok = c.ghost(rel, ".card {}\n\n|\n", 4)
	if !ok || got != ".badge {}" {
		t.Errorf("top level: %q", got)
	}
	// Not on a line with text.
	if got, _, ok = c.ghost(rel, ".card {\n  color: |red;\n}\n", 5); ok {
		t.Errorf("on a non-blank line: %q", got)
	}

	// An empty paired file gets the whole skeleton.
	c.open("src/Empty/Empty.scss")
	got, _, ok = c.ghost("src/Empty/Empty.scss", "|", 2)
	if !ok || got != ".list {\n  &__item {}\n}" {
		t.Errorf("empty file: %q", got)
	}

	// A CSS module gets the styles.x names it lacks.
	c.open("src/Mod/Mod.module.scss")
	got, _, ok = c.ghost("src/Mod/Mod.module.scss", ".ok {}\n\n|", 2)
	if !ok || got != ".root {}\n\n.isOpen {}" {
		t.Errorf("module: %q", got)
	}

	// Turned off by settings.
	c.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"ghostText": map[string]any{"enabled": false}}})
	time.Sleep(50 * time.Millisecond)
	if got, _, ok = c.ghost("src/Empty/Empty.scss", "|", 3); ok {
		t.Errorf("ghost text while disabled: %q", got)
	}
}

func TestInsertMissingSelectorsAction(t *testing.T) {
	c := newStylesheetClient(t)
	c.open("src/Card/Card.scss")
	var actions []protocol.CodeAction
	c.call("textDocument/codeAction", map[string]any{
		"textDocument": map[string]any{"uri": c.uri("src/Card/Card.scss")},
		"range":        protocol.Range{},
		"context":      map[string]any{"diagnostics": []any{}},
	}, &actions)
	if len(actions) != 1 || actions[0].Title != "Insert 4 missing BEM selectors from Card.tsx" {
		t.Fatalf("actions %+v", actions)
	}
	after := c.applyEdit(*actions[0].Edit)["src/Card/Card.scss"]
	for _, want := range []string{"    &--big {}\n  }", "  &__body {}\n  &__text {}\n}", "\n\n.badge {}\n"} {
		if !strings.Contains(after, want) {
			t.Errorf("after insert lacks %q:\n%s", want, after)
		}
	}

	// Empty file: the generated skeleton.
	c.open("src/Empty/Empty.scss")
	c.call("textDocument/codeAction", map[string]any{
		"textDocument": map[string]any{"uri": c.uri("src/Empty/Empty.scss")},
		"range":        protocol.Range{},
		"context":      map[string]any{"diagnostics": []any{}},
	}, &actions)
	if len(actions) != 1 {
		t.Fatalf("empty-file actions %+v", actions)
	}
	if got := c.applyEdit(*actions[0].Edit)["src/Empty/Empty.scss"]; got != ".list {\n  &__item {}\n}\n" {
		t.Errorf("empty file after insert %q", got)
	}
	os.Remove(c.path("src/Empty/Empty.scss"))
}

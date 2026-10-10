package lsp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"scss-bem-gen/internal/core"
	"scss-bem-gen/internal/generator"
	"scss-bem-gen/internal/index"
	"scss-bem-gen/internal/lsp/protocol"
	"scss-bem-gen/internal/scss"
)

// missing describes what a stylesheet lacks for the components paired
// with it.
type missing struct {
	classes    []string     // in DOM order, without duplicates
	roots      []*core.Node // trees of the paired components
	components []string     // their paths
	module     bool         // the stylesheet is used as a CSS module
}

// missingSelectors finds the classes that components paired with the
// stylesheet use but that it (with its @use/@import closure) doesn't
// define. For a CSS module, those are the styles.x names.
func (s *Server) missingSelectors(ix *index.Index, cfg *compiled, sheetPath string) missing {
	var out missing
	defined := map[string]bool{}
	for _, p := range ix.Closure(sheetPath) {
		if d, err := ix.Get(p); err == nil && d.Sheet != nil {
			for _, c := range d.Sheet.Classes {
				defined[c.Name] = true
			}
		}
	}
	seen := map[string]bool{}
	add := func(name string) {
		if !seen[name] && !defined[name] && !cfg.linter.Ignored(name) && generator.ValidIdent(name) {
			seen[name] = true
			out.classes = append(out.classes, name)
		}
	}
	for _, c := range ix.ComponentsUsing(sheetPath) {
		d, err := ix.Get(c)
		if err != nil || d.JSX == nil {
			continue
		}
		paired := containsStr(ix.PrimaryStylesheets(c), sheetPath)
		moduleIdents := map[string]bool{}
		for _, imp := range d.JSX.Stylesheets() {
			if !imp.IsModule() {
				continue
			}
			if p, ok := ix.ResolveStylesheetImport(c, imp.Source); ok && p == sheetPath {
				for _, id := range []string{imp.Default, imp.Namespace} {
					if id != "" {
						moduleIdents[id] = true
					}
				}
			}
		}
		if !paired && len(moduleIdents) == 0 {
			continue // linked only through @use/@import of another sheet
		}
		out.components = append(out.components, c)
		if paired {
			out.roots = append(out.roots, d.JSX.Root)
		}
		if len(moduleIdents) > 0 {
			out.module = true
		}
		walkRefs(d.JSX.Root, func(r core.ClassRef) {
			switch {
			case paired && (r.Kind == core.ClassStatic || r.Kind == core.ClassConditional):
				add(r.Name)
			case r.Kind == core.ClassModule && moduleIdents[r.Module]:
				if !defined[kebabCase(r.Name)] {
					add(r.Name)
				}
			}
		})
	}
	return out
}

// skeleton renders the full skeleton for an empty stylesheet.
func (s *Server) skeleton(cfg *compiled, ms missing) string {
	if ms.module || len(ms.roots) == 0 {
		return addSelectors("", ms.classes, cfg.linter.Naming())
	}
	var parts []string
	for _, r := range ms.roots {
		if g := generator.Generate(r, cfg.genOpts); g != "" {
			parts = append(parts, g)
		}
	}
	return strings.Join(parts, "\n")
}

// ---------------------------------------------------------------------------
// Inline completion (ghost text)

func (s *Server) inlineCompletion(ctx context.Context, p protocol.InlineCompletionParams) (*protocol.InlineCompletionList, error) {
	empty := &protocol.InlineCompletionList{Items: []protocol.InlineCompletionItem{}}
	d, m, off, err := s.docAt(p.TextDocument.URI, p.Position)
	if err != nil || d.Kind != index.KindStylesheet || d.Sheet == nil {
		return empty, nil
	}
	ix, cfg, _ := s.state()
	if !cfg.ghostText || !onBlankLine(d.Text, off) {
		return empty, nil // checked first: this request comes on most keystrokes
	}
	ms := s.missingSelectors(ix, cfg, d.Path)
	if len(ms.classes) == 0 {
		return empty, nil
	}
	insert, start := ghostText(d.Text, d.Sheet, off, ms, cfg, s)
	if insert == "" {
		return empty, nil
	}
	r := m.rangeOfOffsets(start, off)
	return &protocol.InlineCompletionList{Items: []protocol.InlineCompletionItem{{
		InsertText: insert,
		Range:      &r,
		FilterText: d.Text[start:off],
	}}}, nil
}

// ghostText returns the text to suggest at off and where its range starts
// (the range ends at off). It suggests only on blank lines: the whole
// skeleton in an empty file, a rule's missing children inside that rule,
// and missing blocks at the top level.
func ghostText(text string, sheet *scss.Sheet, off int, ms missing, cfg *compiled, s *Server) (string, int) {
	if !onBlankLine(text, off) {
		return "", 0
	}
	lineStart := strings.LastIndexByte(text[:off], '\n') + 1
	if strings.TrimSpace(text) == "" {
		return strings.TrimRight(s.skeleton(cfg, ms), "\n"), off
	}

	node := sheet.NodeAt(off)
	var chunk string
	switch {
	case node.Kind == scss.KindRoot:
		chunk = topLevelChunk(text, sheet, ms.classes, cfg)
	case node.Kind == scss.KindRule && node.Open.Offset < off:
		class := ownClass(sheet, node)
		if class == "" {
			return "", 0
		}
		chunk = childChunk(text, sheet, class, ms.classes, cfg)
	}
	if chunk == "" {
		return "", 0
	}
	chunk = strings.TrimRight(chunk, "\n")
	// Replace from the start of the line when the indentation already typed
	// is a prefix of the chunk; otherwise insert at the cursor.
	if typed := text[lineStart:off]; strings.HasPrefix(chunk, typed) {
		return chunk, lineStart
	}
	return strings.TrimLeft(chunk, " \t"), off
}

func onBlankLine(text string, off int) bool {
	lineStart := strings.LastIndexByte(text[:off], '\n') + 1
	lineEnd := len(text)
	if i := strings.IndexByte(text[off:], '\n'); i >= 0 {
		lineEnd = off + i
	}
	return strings.TrimSpace(text[lineStart:lineEnd]) == ""
}

// ownClass returns the class a rule targets when its only selector is that
// class, e.g. "card__title" for &__title inside .card.
func ownClass(sheet *scss.Sheet, n *scss.Node) string {
	if len(n.Selectors) != 1 || !strings.HasPrefix(n.Selectors[0], ".") {
		return ""
	}
	name := n.Selectors[0][1:]
	for _, c := range sheet.ClassDefs(name) {
		if c.Rule == n {
			return name
		}
	}
	return ""
}

// childChunk returns the lines addSelectors would add directly inside the
// rule for class (and nothing that would go elsewhere).
func childChunk(text string, sheet *scss.Sheet, class string, classes []string, cfg *compiled) string {
	naming := cfg.linter.Naming()
	rule := ruleFor(sheet, class)
	if rule == nil {
		return ""
	}
	insertAt := strings.LastIndexByte(text[:min(rule.Close.Offset, len(text))], '\n') + 1
	if insertAt <= rule.Open.Offset {
		return "" // one-line block: nothing sensible to suggest on a blank line
	}
	cur := text
	for _, c := range classes {
		if !strings.HasPrefix(c, class) || len(c) == len(class) {
			continue
		}
		rest := c[len(class):]
		if !strings.HasPrefix(rest, naming.ElementSep()) && !strings.HasPrefix(rest, naming.ModifierSep()) {
			continue
		}
		next := addSelectors(cur, []string{c}, naming)
		// Accept it only if it was inserted where this rule's children go.
		inserted := len(next) - len(cur)
		pos := insertAt + (len(cur) - len(text))
		if inserted > 0 && next[:pos] == cur[:pos] && next[pos+inserted:] == cur[pos:] {
			cur = next
		}
	}
	if len(cur) == len(text) {
		return ""
	}
	return cur[insertAt : insertAt+len(cur)-len(text)]
}

// topLevelChunk returns new top-level rules for missing classes whose block
// has no rule yet.
func topLevelChunk(text string, sheet *scss.Sheet, classes []string, cfg *compiled) string {
	naming := cfg.linter.Naming()
	var top []string
	for _, c := range classes {
		nm := naming.Parse(c)
		block := nm.Block
		if nm.StructErr || block == "" {
			block = c
		}
		if !sheet.HasClass(block) {
			top = append(top, c)
		}
	}
	if len(top) == 0 {
		return ""
	}
	frag := addSelectors("", top, naming)
	return reindent(frag, "  ", indentUnit(text))
}

// reindent replaces each leading "from" with "to".
func reindent(s, from, to string) string {
	if from == to {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		n := 0
		for strings.HasPrefix(l[n*len(from):], from) {
			n++
		}
		lines[i] = strings.Repeat(to, n) + l[n*len(from):]
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Code action: insert missing selectors (also the fallback for editors
// without inline completion)

func (s *Server) stylesheetActions(d *index.Doc, m *mapper, p protocol.CodeActionParams) []protocol.CodeAction {
	if d.Sheet == nil {
		return nil
	}
	ix, cfg, _ := s.state()
	ms := s.missingSelectors(ix, cfg, d.Path)
	if len(ms.classes) == 0 {
		return nil
	}
	var newText string
	if strings.TrimSpace(d.Text) == "" {
		newText = s.skeleton(cfg, ms)
	} else {
		newText = addSelectors(d.Text, ms.classes, cfg.linter.Naming())
	}
	if newText == d.Text {
		return nil
	}
	var from []string
	for _, c := range ms.components {
		from = append(from, filepath.Base(c))
	}
	title := fmt.Sprintf("Insert %d missing BEM selector", len(ms.classes))
	if len(ms.classes) != 1 {
		title += "s"
	}
	title += " from " + strings.Join(from, ", ")
	return []protocol.CodeAction{{
		Title: title,
		Kind:  protocol.CodeActionSource,
		Edit: &protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{
			pathToURI(d.Path): {diffEdit(m, d.Text, newText)},
		}},
	}}
}

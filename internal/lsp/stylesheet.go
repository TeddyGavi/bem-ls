package lsp

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"scss-bem-gen/internal/bem"
	"scss-bem-gen/internal/core"
	"scss-bem-gen/internal/index"
	"scss-bem-gen/internal/lsp/protocol"
	"scss-bem-gen/internal/scss"
)

// symbolTarget is what a position in a stylesheet refers to.
type symbolTarget struct {
	defs      []index.Def
	ref       *scss.Ref    // the reference under the cursor, if any
	sym       *scss.Symbol // the declaration under the cursor, if any
	namespace string       // set when the cursor is on "ns." of ns.$x
}

// targetAt resolves the symbol at off in sheet (which belongs to path).
func (s *Server) targetAt(ix *index.Index, path string, sheet *scss.Sheet, off int) (symbolTarget, bool) {
	if sym := sheet.SymbolAt(off); sym != nil {
		return symbolTarget{defs: []index.Def{{Path: path, Symbol: sym}}, sym: sym}, true
	}
	ref := sheet.RefAt(off)
	if ref == nil {
		return symbolTarget{}, false
	}
	t := symbolTarget{ref: ref}
	nameStart := ref.Range.End.Offset - len(ref.Kind.Sigil()+ref.Name)
	if ref.Namespace != "" && off < nameStart {
		t.namespace = ref.Namespace
		return t, true
	}
	t.defs = ix.Resolve(path, sheet, ref)
	return t, true
}

// nameRange is the range of the bare name inside a symbol or reference
// range: without the namespace and sigil.
func nameRange(r core.Range, name string) (start, end int) {
	return r.End.Offset - len(name), r.End.Offset
}

// useTarget resolves a @use namespace to the file it loads.
func (s *Server) useTarget(ix *index.Index, path string, sheet *scss.Sheet, ns string) (string, bool) {
	for _, imp := range sheet.Imports {
		if imp.Kind == scss.ImportUse && imp.Namespace == ns {
			return ix.ResolveSass(path, imp.URL)
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Definition, references, highlights

func (s *Server) stylesheetDefinitionIn(ix *index.Index, path string, sheet *scss.Sheet, off int) []index.Location {
	if imp, ok := sheet.ImportAt(off); ok {
		if p, ok := ix.ResolveSass(path, imp.URL); ok {
			return []index.Location{{Path: p}}
		}
		return nil
	}
	t, ok := s.targetAt(ix, path, sheet, off)
	if !ok {
		return nil
	}
	if t.namespace != "" {
		if p, ok := s.useTarget(ix, path, sheet, t.namespace); ok {
			return []index.Location{{Path: p}}
		}
		return nil
	}
	var out []index.Location
	for _, d := range t.defs {
		out = append(out, index.Location{Path: d.Path, Range: d.Symbol.Range})
	}
	return out
}

func (s *Server) stylesheetDefinition(ctx context.Context, d *index.Doc, off int) ([]protocol.Location, error) {
	ix, _, _ := s.state()
	sheet := d.SheetAt(off)
	if sheet == nil {
		return nil, nil
	}
	return s.locations(s.stylesheetDefinitionIn(ix, d.Path, sheet, off)), nil
}

func (s *Server) symbolReferences(ix *index.Index, path string, sheet *scss.Sheet, off int, decl bool) []index.Location {
	t, ok := s.targetAt(ix, path, sheet, off)
	if !ok || len(t.defs) == 0 {
		return nil
	}
	var out []index.Location
	for _, def := range t.defs {
		if decl {
			out = append(out, index.Location{Path: def.Path, Range: def.Symbol.Range})
		}
		out = append(out, ix.References(def)...)
	}
	return out
}

func (s *Server) stylesheetReferences(ctx context.Context, d *index.Doc, off int, decl bool) ([]protocol.Location, error) {
	ix, _, _ := s.state()
	sheet := d.SheetAt(off)
	if sheet == nil {
		return nil, nil
	}
	return s.locations(s.symbolReferences(ix, d.Path, sheet, off, decl)), nil
}

func (s *Server) documentHighlight(ctx context.Context, p protocol.TextDocumentPositionParams) ([]protocol.DocumentHighlight, error) {
	d, m, off, err := s.docAt(p.TextDocument.URI, p.Position)
	if err != nil {
		return nil, nil
	}
	sheet := d.SheetAt(off)
	if sheet == nil {
		return nil, nil
	}
	ix, _, _ := s.state()
	out := []protocol.DocumentHighlight{}
	if c, ok := classDefAt(sheet, off); ok {
		for _, other := range sheet.ClassDefs(c.Name) {
			out = append(out, protocol.DocumentHighlight{Range: m.rangeOf(other.Range), Kind: protocol.HighlightWrite})
		}
		return out, nil
	}
	t, ok := s.targetAt(ix, d.Path, sheet, off)
	if !ok {
		return nil, nil
	}
	for _, def := range t.defs {
		if def.Path == d.Path {
			out = append(out, protocol.DocumentHighlight{Range: m.rangeOf(def.Symbol.Range), Kind: protocol.HighlightWrite})
		}
		// Only this document's references: resolve them directly instead
		// of searching the workspace.
		for _, sh := range d.Sheets() {
			for _, r := range sh.Refs {
				if r.Kind != def.Symbol.Kind || !strings.HasSuffix(r.Name, def.Symbol.Name) {
					continue
				}
				for _, got := range ix.Resolve(d.Path, sh, r) {
					if got.Path == def.Path && got.Symbol.Range == def.Symbol.Range {
						out = append(out, protocol.DocumentHighlight{Range: m.rangeOf(r.Range), Kind: protocol.HighlightRead})
						break
					}
				}
			}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Hover

func (s *Server) stylesheetHover(ix *index.Index, path string, sheet *scss.Sheet, off int) (string, core.Range) {
	if imp, ok := sheet.ImportAt(off); ok {
		if p, ok := ix.ResolveSass(path, imp.URL); ok {
			return fmt.Sprintf("%s `%s` → `%s`", imp.Kind, imp.URL, relPath(ix.Root(), p)), imp.URLRange
		}
		if strings.HasPrefix(imp.URL, "sass:") {
			return fmt.Sprintf("Built-in Sass module `%s`", imp.URL), imp.URLRange
		}
		return fmt.Sprintf("%s `%s`: file not found", imp.Kind, imp.URL), imp.URLRange
	}
	t, ok := s.targetAt(ix, path, sheet, off)
	if !ok {
		return "", core.Range{}
	}
	rng := core.Range{}
	if t.ref != nil {
		rng = t.ref.Range
	} else {
		rng = t.sym.Range
	}
	if t.namespace != "" {
		if p, ok := s.useTarget(ix, path, sheet, t.namespace); ok {
			return fmt.Sprintf("Module `%s` → `%s`", t.namespace, relPath(ix.Root(), p)), rng
		}
		return fmt.Sprintf("Module `%s`", t.namespace), rng
	}
	if len(t.defs) == 0 {
		label := t.ref.Kind.Sigil() + t.ref.Name
		if t.ref.Kind == scss.SymFunction {
			return "", core.Range{} // plain CSS functions: nothing to say
		}
		return fmt.Sprintf("`%s`: no definition found", label), rng
	}
	var b strings.Builder
	for i, def := range t.defs {
		if i > 0 {
			b.WriteString("\n---\n\n")
		}
		b.WriteString(s.describeSymbol(ix, def))
	}
	return b.String(), rng
}

// describeSymbol renders a definition: its declaration, resolved value,
// doc comment and location.
func (s *Server) describeSymbol(ix *index.Index, def index.Def) string {
	sym := def.Symbol
	var b strings.Builder
	b.WriteString("```scss\n")
	switch sym.Kind {
	case scss.SymVariable:
		fmt.Fprintf(&b, "$%s: %s", sym.Name, sym.Value)
		if sym.Default {
			b.WriteString(" !default")
		}
		if !sym.Param {
			b.WriteString(";")
		}
	case scss.SymMixin:
		fmt.Fprintf(&b, "@mixin %s(%s)", sym.Name, sym.Params)
	case scss.SymFunction:
		fmt.Fprintf(&b, "@function %s(%s)", sym.Name, sym.Params)
	case scss.SymPlaceholder:
		fmt.Fprintf(&b, "%%%s", sym.Name)
	case scss.SymCustomProperty:
		fmt.Fprintf(&b, "%s: %s;", sym.Name, sym.Value)
	case scss.SymKeyframes:
		fmt.Fprintf(&b, "@keyframes %s", sym.Name)
	}
	b.WriteString("\n```\n\n")
	if sym.Kind == scss.SymVariable {
		if v, chain := s.resolveValue(ix, def); len(chain) > 0 {
			fmt.Fprintf(&b, "Resolves to `%s` (via %s)\n\n", v, strings.Join(chain, " → "))
		}
	}
	if sym.Param {
		b.WriteString("Parameter\n\n")
	}
	if sym.Doc != "" {
		b.WriteString(sym.Doc + "\n\n")
	}
	fmt.Fprintf(&b, "Defined in `%s:%d`", relPath(ix.Root(), def.Path), sym.Range.Start.Line)
	return b.String()
}

var singleVar = regexp.MustCompile(`^(?:[\w-]+\.)?\$[\w-]+$`)

// resolveValue follows a variable whose value is another variable, up to a
// few hops, and returns the final value and the chain of names.
func (s *Server) resolveValue(ix *index.Index, def index.Def) (string, []string) {
	var chain []string
	cur := def
	for hop := 0; hop < 8 && singleVar.MatchString(cur.Symbol.Value); hop++ {
		d, err := ix.Get(cur.Path)
		if err != nil {
			break
		}
		var next *index.Def
		for _, sh := range d.Sheets() {
			for _, r := range sh.Refs {
				if r.Kind == scss.SymVariable && within(cur.Symbol.Full, r.Range.Start.Offset) {
					if defs := ix.Resolve(cur.Path, sh, r); len(defs) > 0 {
						next = &defs[0]
					}
					break
				}
			}
		}
		if next == nil {
			break
		}
		chain = append(chain, "`"+cur.Symbol.Value+"`")
		cur = *next
	}
	return cur.Symbol.Value, chain
}

// ---------------------------------------------------------------------------
// Rename of symbols

func (s *Server) prepareRenameSymbol(ix *index.Index, d *index.Doc, sheet *scss.Sheet, m *mapper, off int) (*protocol.PrepareRenameResult, error) {
	t, ok := s.targetAt(ix, d.Path, sheet, off)
	if !ok || t.namespace != "" {
		return nil, nil
	}
	if len(t.defs) == 0 {
		return nil, renameError("no definition found for this name")
	}
	var r core.Range
	var name string
	if t.ref != nil {
		r, name = t.ref.Range, t.ref.Name
	} else {
		r, name = t.sym.Range, t.sym.Name
	}
	st, en := nameRange(r, name)
	return &protocol.PrepareRenameResult{Range: m.rangeOfOffsets(st, en), Placeholder: name}, nil
}

var symbolName = regexp.MustCompile(`^-?[A-Za-z_\x80-\x{10FFFF}][\w\-\x80-\x{10FFFF}]*$`)

func (s *Server) renameSymbol(ix *index.Index, enc string, d *index.Doc, sheet *scss.Sheet, off int, newName string) (*protocol.WorkspaceEdit, error) {
	t, ok := s.targetAt(ix, d.Path, sheet, off)
	if !ok || t.namespace != "" {
		return nil, renameError("nothing to rename here")
	}
	if len(t.defs) == 0 {
		return nil, renameError("no definition found for this name")
	}
	def := t.defs[0]
	sym := def.Symbol
	newName = strings.TrimPrefix(strings.TrimPrefix(newName, sym.Kind.Sigil()), sym.Kind.Sigil())
	if sym.Kind == scss.SymCustomProperty && !strings.HasPrefix(newName, "--") {
		newName = "--" + newName
	}
	if !symbolName.MatchString(strings.TrimPrefix(newName, "-")) && !(sym.Kind == scss.SymCustomProperty && symbolName.MatchString(strings.TrimPrefix(newName, "--"))) {
		return nil, renameError("%q is not a valid name", newName)
	}

	edits := map[protocol.DocumentURI][]protocol.TextEdit{}
	mappers := map[string]*mapper{}
	add := func(path string, r core.Range, oldName string) error {
		m := mappers[path]
		if m == nil {
			dd, err := ix.Get(path)
			if err != nil {
				return err
			}
			m = newMapper(dd.Text, enc)
			mappers[path] = m
		}
		st, en := nameRange(r, oldName)
		// A @forward prefix stays: list-reset → list-newname.
		uri := pathToURI(path)
		edits[uri] = append(edits[uri], protocol.TextEdit{Range: m.rangeOfOffsets(st, en), NewText: newName})
		return nil
	}
	if err := add(def.Path, sym.Range, sym.Name); err != nil {
		return nil, err
	}
	for _, l := range ix.References(def) {
		if err := add(l.Path, l.Range, sym.Name); err != nil {
			return nil, err
		}
	}
	return &protocol.WorkspaceEdit{Changes: edits}, nil
}

// ---------------------------------------------------------------------------
// Symbols and folding

func (s *Server) documentSymbol(ctx context.Context, p protocol.DocumentSymbolParams) ([]protocol.DocumentSymbol, error) {
	d, m, _, err := s.docAt(p.TextDocument.URI, protocol.Position{})
	if err != nil || d.Sheet == nil {
		return []protocol.DocumentSymbol{}, nil
	}
	return outline(d.Sheet, m), nil
}

// outline builds the nested symbol tree of a stylesheet: rules and
// at-rules, with the variables, custom properties and placeholders
// declared in them.
func outline(sheet *scss.Sheet, m *mapper) []protocol.DocumentSymbol {
	// Symbols that aren't nodes themselves, grouped by innermost node.
	loose := map[*scss.Node][]*scss.Symbol{}
	for _, sym := range sheet.Symbols {
		if sym.Param || sym.Kind == scss.SymMixin || sym.Kind == scss.SymFunction ||
			sym.Kind == scss.SymKeyframes || sym.Kind == scss.SymPlaceholder {
			continue // shown as the node that declares them
		}
		n := sheet.NodeAt(sym.Range.Start.Offset)
		loose[n] = append(loose[n], sym)
	}
	symKind := map[scss.SymbolKind]int{
		scss.SymVariable: protocol.SymbolKindVariable, scss.SymCustomProperty: protocol.SymbolKindProperty,
	}
	var build func(n *scss.Node) []protocol.DocumentSymbol
	build = func(n *scss.Node) []protocol.DocumentSymbol {
		var out []protocol.DocumentSymbol
		for _, sym := range loose[n] {
			out = append(out, protocol.DocumentSymbol{
				Name: sym.Label(), Detail: sym.Value, Kind: symKind[sym.Kind],
				Range: m.rangeOf(sym.Full), SelectionRange: m.rangeOf(sym.Range),
			})
		}
		for _, c := range n.Children {
			ds := protocol.DocumentSymbol{
				Range:          m.rangeOf(c.Range),
				SelectionRange: m.rangeOf(c.PreludeRange),
				Children:       build(c),
			}
			if c.PreludeRange.End.Offset == c.PreludeRange.Start.Offset {
				ds.SelectionRange = protocol.Range{Start: ds.Range.Start, End: ds.Range.Start}
			}
			switch c.Kind {
			case scss.KindRule:
				ds.Name = collapse(c.Prelude)
				ds.Kind = protocol.SymbolKindClass
				if strings.HasPrefix(c.Prelude, "%") {
					ds.Kind = protocol.SymbolKindInterface
				}
				if len(c.Selectors) > 0 && strings.Join(c.Selectors, ", ") != ds.Name {
					ds.Detail = strings.Join(c.Selectors, ", ")
				}
			default:
				ds.Name = strings.TrimSpace("@" + c.AtName + " " + collapse(c.Prelude))
				ds.Kind = protocol.SymbolKindNamespace
				switch c.AtName {
				case "mixin":
					ds.Kind = protocol.SymbolKindMethod
				case "function":
					ds.Kind = protocol.SymbolKindFunction
				case "keyframes", "-webkit-keyframes":
					ds.Kind = protocol.SymbolKindEvent
				}
			}
			if ds.Name == "" {
				ds.Name = "(anonymous)"
			}
			out = append(out, ds)
		}
		sort.SliceStable(out, func(i, j int) bool {
			a, b := out[i].Range.Start, out[j].Range.Start
			return a.Line < b.Line || (a.Line == b.Line && a.Character < b.Character)
		})
		return out
	}
	out := build(sheet.Root)
	if out == nil {
		out = []protocol.DocumentSymbol{}
	}
	return out
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func (s *Server) workspaceSymbol(ctx context.Context, p protocol.WorkspaceSymbolParams) ([]protocol.SymbolInformation, error) {
	ix, _, enc := s.state()
	q := strings.ToLower(p.Query)
	const limit = 500
	out := []protocol.SymbolInformation{}
	kinds := map[scss.SymbolKind]int{
		scss.SymVariable: protocol.SymbolKindVariable, scss.SymMixin: protocol.SymbolKindMethod,
		scss.SymFunction: protocol.SymbolKindFunction, scss.SymPlaceholder: protocol.SymbolKindInterface,
		scss.SymCustomProperty: protocol.SymbolKindProperty, scss.SymKeyframes: protocol.SymbolKindEvent,
	}
	for _, d := range ix.Docs() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if d.Sheet == nil {
			continue
		}
		var m *mapper
		mapr := func() *mapper {
			if m == nil {
				m = newMapper(d.Text, enc)
			}
			return m
		}
		container := relPath(ix.Root(), d.Path)
		for _, sym := range d.Sheet.Symbols {
			if sym.Scope != nil || sym.Param || !fuzzy(q, sym.Label()) {
				continue
			}
			out = append(out, protocol.SymbolInformation{
				Name: sym.Label(), Kind: kinds[sym.Kind], ContainerName: container,
				Location: protocol.Location{URI: pathToURI(d.Path), Range: mapr().rangeOf(sym.Range)},
			})
		}
		seen := map[string]bool{}
		for _, c := range d.Sheet.Classes {
			if seen[c.Name] || !fuzzy(q, "."+c.Name) {
				continue
			}
			seen[c.Name] = true
			out = append(out, protocol.SymbolInformation{
				Name: "." + c.Name, Kind: protocol.SymbolKindClass, ContainerName: container,
				Location: protocol.Location{URI: pathToURI(d.Path), Range: mapr().rangeOf(c.Range)},
			})
		}
		if len(out) >= limit {
			return out[:limit], nil
		}
	}
	return out, nil
}

// fuzzy reports whether the letters of q appear in s in order
// (case-insensitively).
func fuzzy(q, s string) bool {
	s = strings.ToLower(s)
	i := 0
	for _, r := range s {
		if i < len(q) && rune(q[i]) == r {
			i++
		}
	}
	return i == len(q)
}

func (s *Server) foldingRange(ctx context.Context, p protocol.FoldingRangeParams) ([]protocol.FoldingRange, error) {
	d, m, _, err := s.docAt(p.TextDocument.URI, protocol.Position{})
	if err != nil {
		return []protocol.FoldingRange{}, nil
	}
	out := []protocol.FoldingRange{}
	for _, sheet := range d.Sheets() {
		var walk func(n *scss.Node)
		walk = func(n *scss.Node) {
			for _, c := range n.Children {
				start, end := m.position(c.Open.Offset).Line, m.position(c.Close.Offset).Line
				if end > start {
					// Keep the closing brace visible.
					out = append(out, protocol.FoldingRange{StartLine: start, EndLine: end - 1})
				}
				walk(c)
			}
		}
		walk(sheet.Root)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Diagnostics: unused selectors

func (s *Server) stylesheetDiagnostics(path string, cfg *compiled) []bem.Diagnostic {
	ix, _, _ := s.state()
	defs, used, ok := ix.StylesheetLintInput(path)
	if !ok {
		return nil
	}
	return cfg.linter.LintStylesheet(defs, used)
}

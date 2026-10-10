package lsp

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"scss-bem-gen/internal/core"
	"scss-bem-gen/internal/index"
	"scss-bem-gen/internal/lsp/protocol"
	"scss-bem-gen/internal/scss"
)

// ---------------------------------------------------------------------------
// Finding things at an offset

func within(r core.Range, off int) bool { return r.Start.Offset <= off && off <= r.End.Offset }

// classAt returns the class reference at off in a JSX tree.
func classAt(n *core.Node, off int) (*core.Node, core.ClassRef, bool) {
	for _, c := range n.Classes {
		if within(c.Range, off) {
			return n, c, true
		}
	}
	for _, k := range n.Children {
		if kn, c, ok := classAt(k, off); ok {
			return kn, c, true
		}
	}
	return nil, core.ClassRef{}, false
}

// tagAt returns the element whose opening tag name contains off.
func tagAt(n *core.Node, off int) *core.Node {
	if !n.IsRoot() && n.Tag != "" && within(n.TagRange, off) {
		return n
	}
	for _, k := range n.Children {
		if t := tagAt(k, off); t != nil {
			return t
		}
	}
	return nil
}

// elementAt returns the innermost element whose range contains off.
func elementAt(n *core.Node, off int) *core.Node {
	for _, k := range n.Children {
		end := k.Range.End.Offset
		if k.Range.Start.Offset <= off && (off <= end || end == 0) {
			if inner := elementAt(k, off); inner != nil {
				return inner
			}
			return k
		}
	}
	return nil
}

func classDefAt(sh *scss.Sheet, off int) (scss.ClassDef, bool) {
	for _, c := range sh.Classes {
		if within(c.Range, off) {
			return c, true
		}
	}
	return scss.ClassDef{}, false
}

// ---------------------------------------------------------------------------
// Hover

func (s *Server) hover(ctx context.Context, p protocol.TextDocumentPositionParams) (*protocol.Hover, error) {
	d, m, off, err := s.docAt(p.TextDocument.URI, p.Position)
	if err != nil {
		return nil, nil
	}
	ix, cfg, _ := s.state()
	var md string
	var rng core.Range
	switch d.Kind {
	case index.KindJSX:
		if sh := d.SheetAt(off); sh != nil {
			md, rng = s.stylesheetHover(ix, d.Path, sh, off)
		} else if _, ref, ok := classAt(d.JSX.Root, off); ok {
			md, rng = s.hoverClass(ix, cfg, d, ref), ref.Range
		} else if n := tagAt(d.JSX.Root, off); n != nil {
			if path, def := ix.ResolveStyled(d.Path, n.Tag); def != nil {
				md, rng = s.hoverStyled(ix, path, def), n.TagRange
			}
		}
	case index.KindStylesheet:
		if d.Sheet == nil {
			return nil, nil
		}
		if c, ok := classDefAt(d.Sheet, off); ok {
			md, rng = s.hoverClassDef(ix, d, c), c.Range
		} else {
			md, rng = s.stylesheetHover(ix, d.Path, d.Sheet, off)
		}
	}
	if md == "" {
		return nil, nil
	}
	r := m.rangeOf(rng)
	return &protocol.Hover{Contents: s.markup(md), Range: &r}, nil
}

func (s *Server) markup(md string) protocol.MarkupContent {
	s.mu.Lock()
	markdown := s.caps.markdown
	s.mu.Unlock()
	if markdown {
		return protocol.MarkupContent{Kind: "markdown", Value: md}
	}
	return protocol.MarkupContent{Kind: "plaintext", Value: strings.ReplaceAll(md, "`", "")}
}

func (s *Server) hoverClass(ix *index.Index, cfg *compiled, d *index.Doc, ref core.ClassRef) string {
	var b strings.Builder
	switch ref.Kind {
	case core.ClassModule:
		fmt.Fprintf(&b, "**`%s.%s`** · CSS module\n\n", ref.Module, ref.Name)
	case core.ClassDynamic:
		fmt.Fprintf(&b, "**`%s…`** · dynamic\n\nOnly the prefix is known at compile time.\n\n", ref.Name)
	default:
		fmt.Fprintf(&b, "**`.%s`** · %s\n\n", ref.Name, ref.Kind)
	}
	if ref.Kind != core.ClassModule && ref.Kind != core.ClassDynamic {
		nm := cfg.linter.Naming().Parse(ref.Name)
		if nm.Valid() {
			parts := []string{"block `" + nm.Block + "`"}
			for _, e := range nm.Elements {
				parts = append(parts, "element `"+e+"`")
			}
			if nm.HasMod {
				mod := "modifier `" + nm.Mod + "`"
				if nm.HasVal {
					mod += " = `" + nm.Val + "`"
				}
				parts = append(parts, mod)
			}
			fmt.Fprintf(&b, "%s\n\n", strings.Join(parts, " · "))
		} else {
			fmt.Fprintf(&b, "Not valid %s BEM: %v\n\n", cfg.linter.Naming().Preset(), nm.Err)
		}
	}

	if ref.Kind == core.ClassDynamic {
		var matches []string
		for _, c := range ix.AvailableClasses(d.Path) {
			if strings.HasPrefix(c.Def.Name, ref.Name) && !containsStr(matches, c.Def.Name) {
				matches = append(matches, c.Def.Name)
			}
		}
		if len(matches) > 0 {
			sort.Strings(matches)
			fmt.Fprintf(&b, "Matching classes: %s\n", "`."+strings.Join(matches, "`, `.")+"`")
		}
		return b.String()
	}

	defs := ix.ClassDefinitions(d.Path, ref)
	if len(defs) == 0 {
		switch {
		case ref.Kind == core.ClassModule:
			if imp, ok := d.JSX.ModuleImport(ref.Module); ok {
				fmt.Fprintf(&b, "No `.%s` in `%s`.\n", ref.Name, imp.Source)
			} else {
				fmt.Fprintf(&b, "`%s` is not bound to a stylesheet import.\n", ref.Module)
			}
		case len(ix.PrimaryStylesheets(d.Path)) == 0:
			b.WriteString("No stylesheet found for this component.\n")
		default:
			fmt.Fprintf(&b, "Not defined in %s.\n", s.fileList(ix.PrimaryStylesheets(d.Path)))
		}
		return b.String()
	}
	for i, def := range defs {
		if i == 5 {
			fmt.Fprintf(&b, "\n…and %d more\n", len(defs)-5)
			break
		}
		b.WriteString(s.describeDef(ix, def))
	}
	return b.String()
}

// describeDef renders "Defined in file:line as `&__x` → `.full`".
func (s *Server) describeDef(ix *index.Index, def index.Location) string {
	sd, err := ix.Get(def.Path)
	if err != nil || sd.Sheet == nil {
		return ""
	}
	src := sd.Text[def.Range.Start.Offset:def.Range.End.Offset]
	line := fmt.Sprintf("Defined in `%s:%d` as `%s`", relPath(ix.Root(), def.Path), def.Range.Start.Line, src)
	for _, c := range sd.Sheet.Classes {
		if c.Range == def.Range && c.Rule != nil && len(c.Rule.Selectors) > 0 {
			sels := c.Rule.Selectors
			if len(sels) > 3 {
				sels = append(sels[:3:3], "…")
			}
			line += " → `" + strings.Join(sels, "`, `") + "`"
			break
		}
	}
	return line + "\n\n"
}

func (s *Server) hoverStyled(ix *index.Index, path string, def *core.StyledDef) string {
	var b strings.Builder
	what := "styled." + def.Tag
	if def.Extends != "" {
		what = "styled(" + def.Extends + ")"
	}
	fmt.Fprintf(&b, "**`%s`** · %s\n\nDefined in `%s:%d`\n", def.Name, what, relPath(ix.Root(), path), def.Range.Start.Line)
	if d, err := ix.Get(path); err == nil {
		css := strings.TrimSpace(d.Text[def.Template.Start.Offset:def.Template.End.Offset])
		if len(css) > 800 {
			css = css[:800] + "\n…"
		}
		if css != "" {
			fmt.Fprintf(&b, "\n```scss\n%s\n```\n", css)
		}
	}
	return b.String()
}

func (s *Server) hoverClassDef(ix *index.Index, d *index.Doc, c scss.ClassDef) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**`.%s`**", c.Name)
	if c.Rule != nil && len(c.Rule.Selectors) > 0 {
		fmt.Fprintf(&b, " → `%s`", strings.Join(c.Rule.Selectors, "`, `"))
	}
	b.WriteString("\n\n")
	usages := ix.ClassUsages(d.Path, c.Name)
	switch {
	case len(ix.ComponentsUsing(d.Path)) == 0:
		b.WriteString("No component is linked to this stylesheet.\n")
	case len(usages) == 0:
		b.WriteString("Not used by any linked component.\n")
	default:
		fmt.Fprintf(&b, "Used %d time(s):", len(usages))
		for i, u := range usages {
			if i == 5 {
				b.WriteString(" …")
				break
			}
			fmt.Fprintf(&b, " `%s:%d`", relPath(ix.Root(), u.Path), u.Range.Start.Line)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func (s *Server) fileList(paths []string) string {
	ix, _, _ := s.state()
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = "`" + relPath(ix.Root(), p) + "`"
	}
	return strings.Join(names, ", ")
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Completion

func (s *Server) completion(ctx context.Context, p protocol.CompletionParams) (*protocol.CompletionList, error) {
	d, m, off, err := s.docAt(p.TextDocument.URI, p.Position)
	if err != nil || d.Kind != index.KindJSX {
		return nil, nil
	}
	ix, cfg, _ := s.state()
	isModule := func(id string) bool { return cfg.isModuleIdent(d.JSX, id) }
	cc := core.CompletionContextAt(d.Text, off, cfg.settings.JSX.ClassAttributes, isModule)
	list := &protocol.CompletionList{Items: []protocol.CompletionItem{}}
	switch cc.Kind {
	case core.CompleteClass:
		list.Items = s.classCompletions(ix, cfg, d, m, off, cc)
	case core.CompleteModuleMember:
		list.Items = s.moduleCompletions(ix, d, m, cc)
	}
	return list, nil
}

func (s *Server) classCompletions(ix *index.Index, cfg *compiled, d *index.Doc, m *mapper, off int, cc core.CompletionContext) []protocol.CompletionItem {
	naming := cfg.linter.Naming()
	edit := m.rangeOfOffsets(cc.Start, cc.End)
	seen := map[string]bool{}
	for _, sib := range cc.Siblings {
		seen[sib] = true
	}

	// The block of the nearest ancestor (or this element) ranks first.
	nearBlock := ""
	for n := elementAt(d.JSX.Root, off); n != nil && !n.IsRoot() && nearBlock == ""; n = n.Parent {
		for _, c := range n.Classes {
			if c.Kind != core.ClassStatic && c.Kind != core.ClassConditional {
				continue
			}
			if nm := naming.Parse(c.Name); !nm.StructErr && nm.Block != "" && !within(c.Range, off) {
				nearBlock = nm.Block
				break
			}
		}
	}

	var items []protocol.CompletionItem
	add := func(label, detail, sortKey string, doc string) {
		if seen[label] || !strings.HasPrefix(label, cc.Prefix) {
			return
		}
		seen[label] = true
		it := protocol.CompletionItem{
			Label:    label,
			Kind:     protocol.CompletionKindClass,
			Detail:   detail,
			SortText: sortKey + label,
			TextEdit: &protocol.TextEdit{Range: edit, NewText: label},
		}
		if doc != "" {
			md := s.markup(doc)
			it.Documentation = &md
		}
		items = append(items, it)
	}

	// Element prefix for the nearest block: "card__" re-triggers completion.
	if nearBlock != "" {
		prefix := nearBlock + naming.ElementSep()
		if strings.HasPrefix(prefix, cc.Prefix) && prefix != cc.Prefix {
			it := protocol.CompletionItem{
				Label:    prefix,
				Kind:     protocol.CompletionKindSnippet,
				Detail:   "new element of " + nearBlock,
				SortText: "0" + prefix,
				TextEdit: &protocol.TextEdit{Range: edit, NewText: prefix},
			}
			s.mu.Lock()
			if s.caps.vscode {
				// Re-open the suggestion list after inserting "block__".
				it.Command = &protocol.Command{Title: "Suggest", Command: "editor.action.triggerSuggest"}
			}
			s.mu.Unlock()
			items = append(items, it)
		}
	}

	for _, c := range ix.AvailableClasses(d.Path) {
		key := "2"
		if nm := naming.Parse(c.Def.Name); nearBlock != "" && nm.Block == nearBlock {
			key = "1"
		}
		sel := ""
		if c.Def.Rule != nil {
			sel = "`" + strings.Join(c.Def.Rule.Selectors, "`, `") + "`"
		}
		add(c.Def.Name, filepath.Base(c.Path), key, sel)
	}
	// Classes used elsewhere in this file but not (yet) in a stylesheet.
	var walk func(n *core.Node)
	walk = func(n *core.Node) {
		for _, c := range n.Classes {
			if (c.Kind == core.ClassStatic || c.Kind == core.ClassConditional) && !within(c.Range, off) {
				add(c.Name, "used in this file", "3", "")
			}
		}
		for _, k := range n.Children {
			walk(k)
		}
	}
	walk(d.JSX.Root)
	return items
}

func (s *Server) moduleCompletions(ix *index.Index, d *index.Doc, m *mapper, cc core.CompletionContext) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	seen := map[string]bool{}
	for _, c := range ix.ModuleClasses(d.Path, cc.Module) {
		name := c.Def.Name
		if seen[name] || !strings.HasPrefix(name, cc.Prefix) {
			continue
		}
		seen[name] = true
		it := protocol.CompletionItem{
			Label:    name,
			Kind:     protocol.CompletionKindProperty,
			Detail:   filepath.Base(c.Path),
			SortText: name,
		}
		switch {
		case cc.Bracket:
			it.TextEdit = &protocol.TextEdit{Range: m.rangeOfOffsets(cc.Start, cc.End), NewText: name}
		case isJSIdent(name):
			it.TextEdit = &protocol.TextEdit{Range: m.rangeOfOffsets(cc.Start, cc.End), NewText: name}
		default:
			// styles.my-item is not valid JS: switch to styles['my-item'].
			it.TextEdit = &protocol.TextEdit{Range: m.rangeOfOffsets(cc.Dot, cc.End), NewText: "['" + name + "']"}
			it.FilterText = "." + name
		}
		items = append(items, it)
	}
	return items
}

func isJSIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		ok := r == '_' || r == '$' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || r > 127
		if i > 0 {
			ok = ok || ('0' <= r && r <= '9')
		}
		if !ok {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Definition and references

func (s *Server) definition(ctx context.Context, p protocol.TextDocumentPositionParams) ([]protocol.Location, error) {
	d, _, off, err := s.docAt(p.TextDocument.URI, p.Position)
	if err != nil {
		return nil, nil
	}
	ix, _, _ := s.state()
	var locs []index.Location
	switch d.Kind {
	case index.KindJSX:
		if sh := d.SheetAt(off); sh != nil {
			locs = s.stylesheetDefinitionIn(ix, d.Path, sh, off)
		} else if _, ref, ok := classAt(d.JSX.Root, off); ok && ref.Kind != core.ClassDynamic {
			locs = ix.ClassDefinitions(d.Path, ref)
		} else if n := tagAt(d.JSX.Root, off); n != nil {
			if path, def := ix.ResolveStyled(d.Path, n.Tag); def != nil {
				locs = []index.Location{{Path: path, Range: styledTarget(def)}}
			}
		} else {
			for _, imp := range d.JSX.Stylesheets() {
				if within(imp.SourceRange, off) {
					if p, ok := ix.ResolveStylesheetImport(d.Path, imp.Source); ok {
						locs = append(locs, index.Location{Path: p})
					}
				}
			}
		}
	case index.KindStylesheet:
		return s.stylesheetDefinition(ctx, d, off)
	}
	return s.locations(locs), nil
}

func styledTarget(def *core.StyledDef) core.Range {
	if def.Name != "" {
		return def.NameRange
	}
	return def.Range
}

func (s *Server) locations(locs []index.Location) []protocol.Location {
	out := []protocol.Location{}
	seen := map[string]bool{}
	for _, l := range locs {
		key := fmt.Sprintf("%s:%d:%d", l.Path, l.Range.Start.Offset, l.Range.End.Offset)
		if seen[key] {
			continue
		}
		seen[key] = true
		if pl, ok := s.location(l); ok {
			out = append(out, pl)
		}
	}
	return out
}

func (s *Server) references(ctx context.Context, p protocol.ReferenceParams) ([]protocol.Location, error) {
	d, _, off, err := s.docAt(p.TextDocument.URI, p.Position)
	if err != nil {
		return nil, nil
	}
	ix, _, _ := s.state()
	decl := p.Context.IncludeDeclaration
	var locs []index.Location
	switch d.Kind {
	case index.KindJSX:
		if sh := d.SheetAt(off); sh != nil {
			locs = s.symbolReferences(ix, d.Path, sh, off, decl)
		} else if _, ref, ok := classAt(d.JSX.Root, off); ok && ref.Kind != core.ClassDynamic {
			locs = s.classReferences(ix, d, ref, decl)
		} else if n := tagAt(d.JSX.Root, off); n != nil {
			if path, def := ix.ResolveStyled(d.Path, n.Tag); def != nil {
				locs = s.styledReferences(ix, path, def, decl)
			}
		} else if def := styledNameAt(d.JSX, off); def != nil {
			locs = s.styledReferences(ix, d.Path, def, decl)
		}
	case index.KindStylesheet:
		if d.Sheet == nil {
			return nil, nil
		}
		if c, ok := classDefAt(d.Sheet, off); ok {
			locs = ix.ClassUsages(d.Path, c.Name)
			if decl {
				for _, other := range d.Sheet.ClassDefs(c.Name) {
					locs = append(locs, index.Location{Path: d.Path, Range: other.Range})
				}
			}
		} else {
			return s.stylesheetReferences(ctx, d, off, decl)
		}
	}
	return s.locations(locs), nil
}

// classReferences returns the uses of a class across components, plus its
// definitions if decl is set. Without a definition, only this file is
// searched.
func (s *Server) classReferences(ix *index.Index, d *index.Doc, ref core.ClassRef, decl bool) []index.Location {
	defs := ix.ClassDefinitions(d.Path, ref)
	var out []index.Location
	if len(defs) == 0 {
		walkRefs(d.JSX.Root, func(c core.ClassRef) {
			if c.Name == ref.Name && c.Kind == ref.Kind && c.Module == ref.Module {
				out = append(out, index.Location{Path: d.Path, Range: c.Range})
			}
		})
		return out
	}
	for _, def := range defs {
		// The class name at the definition (it differs from ref.Name for a
		// camelCase CSS-module alias), not the "&__x" source text.
		name := ref.Name
		if sd, err := ix.Get(def.Path); err == nil && sd.Sheet != nil {
			for _, c := range sd.Sheet.Classes {
				if c.Range == def.Range {
					name = c.Name
				}
			}
		}
		out = append(out, ix.ClassUsages(def.Path, name)...)
		if decl {
			out = append(out, def)
		}
	}
	return out
}

func walkRefs(n *core.Node, fn func(core.ClassRef)) {
	for _, c := range n.Classes {
		fn(c)
	}
	for _, k := range n.Children {
		walkRefs(k, fn)
	}
}

// styledNameAt returns the styled definition whose variable name is at off.
func styledNameAt(f *core.File, off int) *core.StyledDef {
	for _, def := range f.Styled {
		if def.Name != "" && within(def.NameRange, off) {
			return def
		}
	}
	return nil
}

// styledReferences finds every JSX element rendering a styled definition.
func (s *Server) styledReferences(ix *index.Index, defPath string, def *core.StyledDef, decl bool) []index.Location {
	var out []index.Location
	if decl {
		out = append(out, index.Location{Path: defPath, Range: styledTarget(def)})
	}
	for _, d := range ix.Docs() {
		if d.Kind != index.KindJSX || d.JSX == nil {
			continue
		}
		var walk func(n *core.Node)
		walk = func(n *core.Node) {
			if !n.IsRoot() && n.IsComponent() {
				if p, got := ix.ResolveStyled(d.Path, n.Tag); got != nil && p == defPath &&
					got.Range.Start.Offset == def.Range.Start.Offset {
					out = append(out, index.Location{Path: d.Path, Range: n.TagRange})
				}
			}
			for _, k := range n.Children {
				walk(k)
			}
		}
		walk(d.JSX.Root)
	}
	return out
}

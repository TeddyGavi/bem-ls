package core

import (
	"path"
	"strings"
)

// File is a parsed JSX/TSX source file.
type File struct {
	Name    string
	Source  string
	Root    *Node
	Imports []Import
	Styled  []*StyledDef
}

// ParseFile lexes and parses src. The File is returned even when err is not
// nil, so callers can use partial results.
func ParseFile(name, src string) (*File, error) {
	return ParseFileWith(name, src, nil)
}

// ParseFileWith is ParseFile with a hook that can change the parser's
// settings (ClassAttrs, ModuleIdents, ...) before parsing.
func ParseFileWith(name, src string, configure func(*Parser)) (*File, error) {
	_, tokens := Lex(name, src)
	p := NewParser(tokens)
	if configure != nil {
		configure(p)
	}
	err := p.Parse()
	return p.File(name, src), err
}

// File returns the parse result. name and src are recorded as given; src
// must be the input the tokens came from for TemplateCSS to work.
func (p *Parser) File(name, src string) *File {
	f := &File{Name: name, Source: src, Root: p.root, Imports: p.imports, Styled: p.styled}
	f.linkStyled()
	return f
}

// ModuleImport returns the stylesheet import bound to ident, as in
// `import styles from './x.module.scss'`.
func (f *File) ModuleImport(ident string) (Import, bool) {
	for _, imp := range f.Imports {
		if imp.IsStylesheet() && (imp.Default == ident || imp.Namespace == ident) {
			return imp, true
		}
	}
	return Import{}, false
}

// Stylesheets returns every import of a .css, .scss, .sass or .less file.
func (f *File) Stylesheets() []Import {
	var out []Import
	for _, imp := range f.Imports {
		if imp.IsStylesheet() {
			out = append(out, imp)
		}
	}
	return out
}

// StyledByName returns the styled definition assigned to name, or nil.
func (f *File) StyledByName(name string) *StyledDef {
	for _, d := range f.Styled {
		if d.Name == name {
			return d
		}
	}
	return nil
}

// TemplateCSS returns the CSS text of d's template. Each ${...} hole is
// replaced by underscores of the same byte length (newlines are kept), so
// offsets and line numbers inside the result match the source.
func (f *File) TemplateCSS(d *StyledDef) string {
	s, e := d.Template.Start.Offset, d.Template.End.Offset
	if s < 0 || e > len(f.Source) || s > e {
		return ""
	}
	b := []byte(f.Source[s:e])
	for _, h := range d.Holes {
		for i := h.Start.Offset - s; i < h.End.Offset-s && i < len(b); i++ {
			if i >= 0 && b[i] != '\n' {
				b[i] = '_'
			}
		}
	}
	return string(b)
}

// linkStyled points JSX elements at the styled component they render.
func (f *File) linkStyled() {
	byName := map[string]*StyledDef{}
	for _, d := range f.Styled {
		if d.Kind == StyledComponent && d.Name != "" {
			byName[d.Name] = d
		}
	}
	if len(byName) == 0 {
		return
	}
	var walk func(n *Node)
	walk = func(n *Node) {
		if d, ok := byName[n.Tag]; ok {
			n.Styled = d
			d.Usages = append(d.Usages, n)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(f.Root)
}

// ---------------------------------------------------------------------------
// Imports

// Import is an import statement or a `x = require('...')` call.
type Import struct {
	Source      string // module specifier as written, e.g. "./Card.module.scss"
	SourceRange Range  // range of Source, without quotes
	Default     string // import styles from '...' or styles = require('...')
	Namespace   string // import * as styles from '...'
	Named       []ImportName
	TypeOnly    bool // import type ...
	Require     bool
	Range       Range
}

// ImportName is one entry of `import { a as b }`.
type ImportName struct {
	Name  string // name exported by the module (a)
	Local string // local binding (b, or a if there is no alias)
	Range Range  // range of Local
}

var stylesheetExts = []string{".css", ".scss", ".sass", ".less"}

// IsStylesheet reports whether the import is of a stylesheet.
func (i Import) IsStylesheet() bool {
	ext := strings.ToLower(path.Ext(stripQuery(i.Source)))
	return contains(stylesheetExts, ext)
}

// IsModule reports whether the import is a CSS module: a stylesheet named
// *.module.* or bound to an identifier (plain stylesheets have no exports).
func (i Import) IsModule() bool {
	if !i.IsStylesheet() {
		return false
	}
	base := path.Base(stripQuery(i.Source))
	return strings.Contains(base, ".module.") || i.Default != "" || i.Namespace != ""
}

func stripQuery(s string) string {
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		return s[:i]
	}
	return s
}

// parseImport parses an import statement after the `import` keyword. On
// anything unexpected it puts the token back and records nothing.
func (p *Parser) parseImport(kw Item) {
	imp := Import{Range: Range{Start: startPos(kw)}}
	t := p.next()
	if t.Typ == itemIdent && t.Val == "type" {
		if n := p.peek(); n.Typ == itemIdent && n.Val != "from" || n.Typ == itemOpenBrace || isPunctItem(n, "*") {
			imp.TypeOnly = true
			t = p.next()
		}
	}
	if t.Typ == itemString { // import './x.css'
		p.finishImport(&imp, t)
		return
	}
	if t.Typ == itemIdent && t.Val != "from" {
		imp.Default = t.Val
		if n := p.peek(); !isPunctItem(n, ",") {
			p.expectFrom(&imp)
			return
		}
		p.next()
		t = p.next()
	}
	switch {
	case isPunctItem(t, "*"):
		as, ns := p.next(), p.next()
		if as.Typ != itemIdent || as.Val != "as" || ns.Typ != itemIdent {
			p.unread(ns)
			p.unread(as)
			return
		}
		imp.Namespace = ns.Val
	case t.Typ == itemOpenBrace:
		if !p.parseNamedImports(&imp) {
			return
		}
	default:
		p.unread(t) // import(...), import.meta, or a syntax error
		return
	}
	p.expectFrom(&imp)
}

func (p *Parser) parseNamedImports(imp *Import) bool {
	for {
		t := p.next()
		switch {
		case t.Typ == itemCloseBrace:
			return true
		case isPunctItem(t, ","):
		case t.Typ == itemIdent && t.Val == "type" && p.peek().Typ == itemIdent && p.peek().Val != "as":
			// import { type A }
		case t.Typ == itemIdent || t.Typ == itemString:
			n := ImportName{Name: t.Val, Local: t.Val, Range: rangeOf(t)}
			if a := p.peek(); a.Typ == itemIdent && a.Val == "as" {
				p.next()
				local := p.next()
				if local.Typ != itemIdent {
					p.unread(local)
					return false
				}
				n.Local, n.Range = local.Val, rangeOf(local)
			}
			imp.Named = append(imp.Named, n)
		default:
			p.unread(t)
			return false
		}
	}
}

func (p *Parser) expectFrom(imp *Import) {
	from := p.next()
	if from.Typ != itemIdent || from.Val != "from" {
		p.unread(from)
		return
	}
	src := p.next()
	if src.Typ != itemString {
		p.unread(src)
		return
	}
	p.finishImport(imp, src)
}

func (p *Parser) finishImport(imp *Import, src Item) {
	imp.Source = src.Val
	imp.SourceRange = rangeOf(src)
	imp.Range.End = endPos(src)
	p.addImport(*imp)
}

func (p *Parser) addImport(imp Import) {
	p.imports = append(p.imports, imp)
	if imp.IsStylesheet() {
		for _, id := range []string{imp.Default, imp.Namespace} {
			if id != "" && !contains(p.moduleIdents, id) {
				p.moduleIdents = append(p.moduleIdents, id)
			}
		}
	}
}

// observeRequire records `x = require('s')` and `require('s')` when the
// closing parenthesis arrives.
func (p *Parser) observeRequire() {
	r := p.recent
	n := len(r)
	// r[n-1] is ")"
	if n < 4 || r[n-2].Typ != itemString || !isPunctItem(r[n-3], "(") ||
		r[n-4].Typ != itemIdent || r[n-4].Val != "require" || (n >= 5 && r[n-5].Typ == itemDot) {
		return
	}
	src := r[n-2]
	imp := Import{
		Source:      src.Val,
		SourceRange: rangeOf(src),
		Require:     true,
		Range:       Range{Start: startPos(r[n-4]), End: endPos(r[n-1])},
	}
	if n >= 6 && isPunctItem(r[n-5], "=") && r[n-6].Typ == itemIdent {
		imp.Default = r[n-6].Val
		imp.Range.Start = startPos(r[n-6])
	}
	p.addImport(imp)
}

// ---------------------------------------------------------------------------
// styled-components, emotion and friends

// StyledKind is the kind of a CSS-in-JS tagged template.
type StyledKind int

const (
	// StyledComponent is styled.tag`...` or styled(Component)`...`.
	StyledComponent StyledKind = iota
	// StyledCSS is css`...`.
	StyledCSS
	// StyledKeyframes is keyframes`...`.
	StyledKeyframes
	// StyledGlobal is createGlobalStyle`...` or injectGlobal`...`.
	StyledGlobal
)

func (k StyledKind) String() string {
	switch k {
	case StyledComponent:
		return "styled"
	case StyledCSS:
		return "css"
	case StyledKeyframes:
		return "keyframes"
	case StyledGlobal:
		return "global"
	}
	return "StyledKind(?)"
}

// DefaultCSSHelpers maps template tags to the kind of CSS they hold.
var DefaultCSSHelpers = map[string]StyledKind{
	"css":               StyledCSS,
	"keyframes":         StyledKeyframes,
	"createGlobalStyle": StyledGlobal,
	"injectGlobal":      StyledGlobal,
}

// StyledDef is a CSS-in-JS tagged template.
type StyledDef struct {
	Kind      StyledKind
	Name      string // variable it is assigned to; "" if anonymous
	NameRange Range
	Tag       string // "button" for styled.button`` or styled('button')``
	Extends   string // "Button" for styled(Button)``; "" if not a name
	Range     Range  // from the tag expression to the closing backtick
	Template  Range  // the text between the backticks
	Holes     []Range
	Usages    []*Node // JSX elements in the same file rendering it
}

// templateFrame tracks an open template literal while parsing.
type templateFrame struct {
	def  *StyledDef // nil for a template that is not CSS
	hole Position   // start of the open ${, if any
}

// recentMax bounds how many tokens are kept for looking back. It covers a
// styled.x.attrs({...})<Props> chain of reasonable length.
const recentMax = 128

// observe sees every token once, in order, as it comes off the channel. It
// finds require() calls and CSS-in-JS templates, wherever they are.
func (p *Parser) observe(it Item) {
	switch it.Typ {
	case itemText:
		return
	case itemTemplateOpen:
		def := p.matchStyled()
		if def != nil {
			def.Template.Start = endPos(it)
		}
		p.templates = append(p.templates, templateFrame{def: def})
	case itemTemplateExprOpen:
		if n := len(p.templates); n > 0 {
			p.templates[n-1].hole = startPos(it)
		}
	case itemTemplateExprClose:
		if n := len(p.templates); n > 0 && p.templates[n-1].def != nil {
			d := p.templates[n-1].def
			d.Holes = append(d.Holes, Range{Start: p.templates[n-1].hole, End: endPos(it)})
		}
	case itemTemplateClose:
		if n := len(p.templates); n > 0 {
			if d := p.templates[n-1].def; d != nil {
				d.Template.End = startPos(it)
				d.Range.End = endPos(it)
				p.styled = append(p.styled, d)
			}
			p.templates = p.templates[:n-1]
		}
	}

	if p.recent == nil {
		p.recent = make([]Item, 0, 2*recentMax)
	}
	if len(p.recent) == cap(p.recent) {
		p.recent = append(p.recent[:0], p.recent[len(p.recent)-recentMax:]...)
	}
	p.recent = append(p.recent, it)
	if isPunctItem(it, ")") {
		p.observeRequire()
	}
}

var styledChainMethods = []string{"attrs", "withConfig"}

// matchStyled checks whether the tokens just before a template's opening
// backtick form a CSS-in-JS tag, and returns a definition if so.
func (p *Parser) matchStyled() *StyledDef {
	r := p.recent
	j := len(r) - 1

	// TypeScript type arguments: styled.div<Props>`...`
	if j >= 0 && isPunctItem(r[j], ">", ">>", ">>>") {
		depth := 0
		for ; j >= 0; j-- {
			switch {
			case isPunctItem(r[j], ">"):
				depth++
			case isPunctItem(r[j], ">>"):
				depth += 2
			case isPunctItem(r[j], ">>>"):
				depth += 3
			case isPunctItem(r[j], "<"):
				depth--
			}
			if depth == 0 {
				break
			}
		}
		j--
	}

	// .attrs(...) and .withConfig(...)
	for j >= 0 && isPunctItem(r[j], ")") {
		k := matchParenBack(r, j)
		if k < 2 || r[k-1].Typ != itemIdent || !contains(styledChainMethods, r[k-1].Val) || r[k-2].Typ != itemDot {
			break
		}
		j = k - 3
	}
	if j < 0 {
		return nil
	}

	def := &StyledDef{}
	start := -1
	notMember := func(i int) bool { return i == 0 || r[i-1].Typ != itemDot }
	switch {
	case j >= 2 && r[j].Typ == itemIdent && r[j-1].Typ == itemDot &&
		r[j-2].Typ == itemIdent && contains(p.StyledIdents, r[j-2].Val) && notMember(j-2):
		def.Kind, def.Tag, start = StyledComponent, r[j].Val, j-2
	case isPunctItem(r[j], ")"):
		k := matchParenBack(r, j)
		if k < 1 || r[k-1].Typ != itemIdent || !contains(p.StyledIdents, r[k-1].Val) || !notMember(k-1) {
			return nil
		}
		def.Kind, start = StyledComponent, k-1
		args := r[k+1 : j]
		switch {
		case len(args) == 1 && args[0].Typ == itemString:
			def.Tag = args[0].Val
		case isDottedName(args):
			var b strings.Builder
			for _, a := range args {
				b.WriteString(a.Val)
			}
			def.Extends = b.String()
		}
	case r[j].Typ == itemIdent && notMember(j):
		kind, ok := p.CSSHelpers[r[j].Val]
		if !ok {
			return nil
		}
		def.Kind, start = kind, j
	default:
		return nil
	}

	def.Range.Start = startPos(r[start])
	if start >= 2 && isPunctItem(r[start-1], "=") && r[start-2].Typ == itemIdent {
		def.Name, def.NameRange = r[start-2].Val, rangeOf(r[start-2])
	}
	return def
}

// matchParenBack returns the index of the "(" matching the ")" at r[j], or
// -1.
func matchParenBack(r []Item, j int) int {
	depth := 0
	for ; j >= 0; j-- {
		switch {
		case isPunctItem(r[j], ")"):
			depth++
		case isPunctItem(r[j], "("):
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// isDottedName reports whether toks spell a name like Button or ui.Button.
func isDottedName(toks []Item) bool {
	if len(toks)%2 == 0 {
		return false
	}
	for i, t := range toks {
		if (i%2 == 0 && t.Typ != itemIdent) || (i%2 == 1 && t.Typ != itemDot) {
			return false
		}
	}
	return true
}

func isPunctItem(it Item, vals ...string) bool {
	return it.Typ == itemPunct && contains(vals, it.Val)
}

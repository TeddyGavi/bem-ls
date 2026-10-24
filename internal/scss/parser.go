package scss

import (
	"fmt"
	"sort"
	"strings"

	"github.com/TeddyGavi/bem-ls/internal/core"
)

// Parse parses SCSS or CSS. It never fails: problems are recorded in
// Sheet.Errors and parsing goes on.
func Parse(name, src string) *Sheet {
	return ParseAt(name, src, core.Position{Line: 1, Col: 1})
}

// ParseAt parses src as if it started at base in a larger document, e.g. the
// CSS of a styled-components template inside a .tsx file. All ranges are in
// the larger document. A top-level & stays literal.
func ParseAt(name, src string, base core.Position) *Sheet {
	if base.Line == 0 {
		base.Line = 1
	}
	if base.Col == 0 {
		base.Col = 1
	}
	_, tokens := LexAt(src, base)
	p := &parser{input: src, base: base, tokens: tokens}
	p.lineStarts = append(p.lineStarts, 0)
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			p.lineStarts = append(p.lineStarts, i+1)
		}
	}
	root := &Node{Kind: KindRoot}
	p.sheet = &Sheet{Name: name, Root: root}
	end := p.parseBlock(root, context{})
	root.Range = core.Range{Start: p.pos(0), End: p.pos(len(src))}
	root.Close = root.Range.End
	if end.Typ == itemPunct {
		p.errorf(p.posOf(end), "unexpected '}'")
	}
	return p.sheet
}

// context is what a block's statements inherit.
type context struct {
	parents   []string // selectors that & refers to; nil at the top level
	scope     *Node    // nil at the top level
	keyframes bool     // inside @keyframes: selectors are from/to/percentages
}

type parser struct {
	input      string
	base       core.Position
	tokens     <-chan Item
	peeked     []Item
	sheet      *Sheet
	lineStarts []int
	doc        []string // doc comments waiting for the next statement
}

// ---------------------------------------------------------------------------
// Token access

func (p *parser) next() Item {
	if n := len(p.peeked); n > 0 {
		it := p.peeked[n-1]
		p.peeked = p.peeked[:n-1]
		return it
	}
	for it := range p.tokens {
		if it.Typ == itemError {
			p.errorf(p.posOf(it), "%s", it.Val)
			continue
		}
		return it
	}
	return Item{Typ: itemEOF, Pos: p.base.Offset + len(p.input)}
}

func (p *parser) unread(it Item) { p.peeked = append(p.peeked, it) }

// nextSignificant skips spaces, collecting doc comments.
func (p *parser) nextSignificant() Item {
	for {
		it := p.next()
		switch it.Typ {
		case itemSpace:
			if strings.Count(it.Val, "\n") > 1 {
				p.doc = nil // a blank line detaches comments
			}
		case itemComment:
			if d, ok := docText(it.Val); ok {
				p.doc = append(p.doc, d)
			} else {
				p.doc = nil
			}
		default:
			return it
		}
	}
}

func (p *parser) takeDoc() string {
	d := strings.Join(p.doc, "\n")
	p.doc = nil
	return d
}

func docText(c string) (string, bool) {
	switch {
	case strings.HasPrefix(c, "///"):
		return strings.TrimSpace(strings.TrimPrefix(c, "///")), true
	case strings.HasPrefix(c, "/**"):
		body := strings.TrimSuffix(strings.TrimPrefix(c, "/**"), "*/")
		var lines []string
		for _, l := range strings.Split(body, "\n") {
			l = strings.TrimSpace(l)
			l = strings.TrimSpace(strings.TrimPrefix(l, "*"))
			if l != "" {
				lines = append(lines, l)
			}
		}
		return strings.Join(lines, "\n"), true
	}
	return "", false
}

func isPunct(it Item, s string) bool { return it.Typ == itemPunct && it.Val == s }

// ---------------------------------------------------------------------------
// Positions

// off converts a document offset into an offset in p.input.
func (p *parser) off(docOffset int) int { return docOffset - p.base.Offset }

// pos converts an offset in p.input into a document position.
func (p *parser) pos(off int) core.Position {
	if off < 0 {
		off = 0
	}
	if off > len(p.input) {
		off = len(p.input)
	}
	i := sort.Search(len(p.lineStarts), func(i int) bool { return p.lineStarts[i] > off }) - 1
	col := off - p.lineStarts[i] + 1
	if i == 0 {
		col += p.base.Col - 1
	}
	return core.Position{Offset: p.base.Offset + off, Line: p.base.Line + i, Col: col}
}

func (p *parser) posOf(it Item) core.Position {
	return core.Position{Offset: it.Pos, Line: it.Line, Col: it.Col}
}

func (p *parser) rangeOf(it Item) core.Range {
	return core.Range{Start: p.posOf(it), End: p.pos(p.off(it.Pos) + len(it.Val))}
}

// rangeOfText is the range of input[start:end], given as input offsets.
func (p *parser) rangeOfText(start, end int) core.Range {
	return core.Range{Start: p.pos(start), End: p.pos(end)}
}

func (p *parser) errorf(pos core.Position, format string, args ...any) {
	p.sheet.Errors = append(p.sheet.Errors, &core.Error{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// ---------------------------------------------------------------------------
// Statements

// stmt is the tokens of one statement up to (not including) its terminator.
type stmt struct {
	toks []Item // without leading spaces/comments
	term Item   // '{', ';', '}' or EOF
}

// startOff and endOff are the input offsets of the statement text.
func (s stmt) startOff(p *parser) int { return p.off(s.toks[0].Pos) }
func (s stmt) endOff(p *parser) int   { return p.off(s.term.Pos) }

// text returns the raw statement text, trimmed, and its input offset.
func (s stmt) text(p *parser) (string, int) {
	if len(s.toks) == 0 {
		return "", p.off(s.term.Pos)
	}
	start, end := s.startOff(p), s.endOff(p)
	if end < start {
		end = start
	}
	raw := p.input[start:end]
	trimmed := strings.TrimRight(raw, " \t\r\n\f")
	return trimmed, start
}

// readStmt reads tokens up to a top-level '{', ';' or '}' (or EOF). first
// is the statement's first significant token, already read.
func (p *parser) readStmt(first Item) stmt {
	s := stmt{toks: []Item{first}}
	depth, interp := 0, 0
	consider := func(it Item) {
		switch {
		case it.Typ == itemInterpOpen:
			interp++
		case isPunct(it, "(") || isPunct(it, "["):
			depth++
		case isPunct(it, ")") || isPunct(it, "]"):
			if depth > 0 {
				depth--
			}
		}
	}
	if isTerminator(first) {
		return stmt{term: first}
	}
	consider(first)
	for {
		it := p.next()
		if it.Typ == itemEOF {
			s.term = it
			return s
		}
		if it.Typ == itemComment {
			continue
		}
		if isPunct(it, "}") && interp > 0 {
			interp--
			s.toks = append(s.toks, it)
			continue
		}
		if depth == 0 && interp == 0 && isTerminator(it) {
			s.term = it
			return s
		}
		consider(it)
		s.toks = append(s.toks, it)
	}
}

func isTerminator(it Item) bool {
	return isPunct(it, "{") || isPunct(it, ";") || isPunct(it, "}") || it.Typ == itemEOF
}

// parseBlock parses statements into n until a '}' or EOF, which it returns.
func (p *parser) parseBlock(n *Node, ctx context) Item {
	for {
		first := p.nextSignificant()
		switch {
		case first.Typ == itemEOF:
			return first
		case isPunct(first, "}"):
			if n.Kind == KindRoot {
				p.errorf(p.posOf(first), "unexpected '}'")
				continue
			}
			return first
		case isPunct(first, ";"):
			p.doc = nil
			continue
		case first.Typ == itemAtKeyword:
			p.parseAtRule(n, ctx, first)
		default:
			s := p.readStmt(first)
			if isPunct(s.term, "{") {
				p.parseStyleRule(n, ctx, s)
			} else {
				p.parseDeclaration(n, ctx, s)
				if isPunct(s.term, "}") {
					p.unread(s.term)
				}
			}
		}
	}
}

// openBlock creates a child node for a statement ending in '{' and parses
// its block.
func (p *parser) openBlock(parent *Node, s stmt, kind NodeKind, atName, prelude string, preludeRange core.Range, selectors []string, ctx context, setup func(*Node)) *Node {
	n := &Node{
		Kind:         kind,
		AtName:       atName,
		Prelude:      prelude,
		PreludeRange: preludeRange,
		Selectors:    selectors,
		Open:         p.posOf(s.term),
		Parent:       parent,
	}
	if len(s.toks) > 0 {
		n.Range.Start = p.posOf(s.toks[0])
	} else {
		n.Range.Start = n.Open
	}
	parent.Children = append(parent.Children, n)
	if setup != nil {
		setup(n)
	}
	ctx.scope = n
	end := p.parseBlock(n, ctx)
	if end.Typ == itemEOF {
		p.errorf(n.Open, "unclosed block")
		n.Close = p.posOf(end)
		n.Range.End = n.Close
	} else {
		n.Close = p.posOf(end)
		n.Range.End = p.pos(p.off(end.Pos) + 1)
	}
	return n
}

func (p *parser) parseStyleRule(parent *Node, ctx context, s stmt) {
	p.doc = nil
	text, off := s.text(p)
	pr := p.rangeOfText(off, off+len(text))
	p.collectRefs(s.toks, ctx.scope, false)

	if ctx.keyframes {
		p.openBlock(parent, s, KindRule, "", text, pr, nil, context{scope: ctx.scope}, nil)
		return
	}
	own := splitSelectorList(span{text: text, off: off})
	selectors := resolveSelectors(ctx.parents, own)
	var hits, placeholders []classHit
	for _, o := range own {
		c, ph := scanSelector(o, ctx.parents)
		hits = append(hits, c...)
		placeholders = append(placeholders, ph...)
	}
	child := ctx
	child.parents = selectors
	p.openBlock(parent, s, KindRule, "", text, pr, selectors, child, func(n *Node) {
		for _, h := range hits {
			p.sheet.Classes = append(p.sheet.Classes, ClassDef{Name: h.name, Range: p.rangeOfText(h.start, h.end), Rule: n})
		}
		for _, h := range placeholders {
			r := p.rangeOfText(h.start, h.end)
			p.sheet.Symbols = append(p.sheet.Symbols, &Symbol{
				Kind: SymPlaceholder, Name: h.name, Range: r, Full: core.Range{Start: r.Start},
			})
		}
	})
	// Full range of placeholders is the whole rule: fix up now it is known.
	if len(placeholders) > 0 {
		n := parent.Children[len(parent.Children)-1]
		for _, sym := range p.sheet.Symbols[len(p.sheet.Symbols)-len(placeholders):] {
			sym.Full = n.Range
		}
	}
}

func (p *parser) parseDeclaration(parent *Node, ctx context, s stmt) {
	doc := p.takeDoc()
	if len(s.toks) == 0 {
		return
	}
	colon := -1
	depth := 0
	for i, t := range s.toks {
		switch {
		case isPunct(t, "(") || isPunct(t, "["):
			depth++
		case isPunct(t, ")") || isPunct(t, "]"):
			depth--
		case isPunct(t, ":") && depth == 0 && colon < 0:
			colon = i
		}
	}
	text, off := s.text(p)
	full := p.rangeOfText(off, off+len(text))
	if isPunct(s.term, ";") {
		full.End = p.pos(p.off(s.term.Pos) + 1)
	}
	if colon < 0 {
		p.collectRefs(s.toks, ctx.scope, true)
		return
	}

	first := s.toks[0]
	valueToks := s.toks[colon+1:]
	value := ""
	if len(valueToks) > 0 {
		vs := p.off(s.toks[colon].Pos) + 1
		value = strings.TrimSpace(p.input[vs : off+len(text)])
	}

	switch {
	case first.Typ == itemVariable && onlySpaceBetween(s.toks, 1, colon):
		sym := &Symbol{Kind: SymVariable, Name: first.Val[1:], Range: p.rangeOf(first), Full: full, Doc: doc}
		v, isDefault, global := stripFlags(value)
		sym.Value, sym.Default = v, isDefault
		if !global {
			sym.Scope = ctx.scope
		}
		p.sheet.Symbols = append(p.sheet.Symbols, sym)
		p.collectRefs(valueToks, ctx.scope, true)
		return
	case first.Typ == itemIdent && strings.HasPrefix(first.Val, "--") && onlySpaceBetween(s.toks, 1, colon):
		p.sheet.Symbols = append(p.sheet.Symbols, &Symbol{
			Kind: SymCustomProperty, Name: first.Val, Range: p.rangeOf(first), Full: full, Value: value, Doc: doc,
		})
	}

	prop := strings.TrimSpace(p.input[off:p.off(s.toks[colon].Pos)])
	if parent.Kind != KindRoot || prop != "" {
		parent.Decls = append(parent.Decls, Decl{
			Property:  prop,
			Value:     value,
			PropRange: p.rangeOfText(off, off+len(prop)),
			Range:     full,
		})
	}
	p.collectRefs(s.toks[:colon], ctx.scope, false)
	p.collectRefs(valueToks, ctx.scope, true)
}

func onlySpaceBetween(toks []Item, from, to int) bool {
	for _, t := range toks[from:to] {
		if t.Typ != itemSpace {
			return false
		}
	}
	return true
}

// stripFlags removes !default and !global from a variable's value.
func stripFlags(v string) (value string, isDefault, isGlobal bool) {
	for {
		t := strings.TrimSpace(v)
		switch {
		case strings.HasSuffix(t, "!default"):
			isDefault, v = true, strings.TrimSuffix(t, "!default")
		case strings.HasSuffix(t, "!global"):
			isGlobal, v = true, strings.TrimSuffix(t, "!global")
		default:
			return t, isDefault, isGlobal
		}
	}
}

// collectRefs records variable, custom-property and (in values) function
// references in toks.
func (p *parser) collectRefs(toks []Item, scope *Node, value bool) {
	sig := make([]Item, 0, len(toks))
	for _, t := range toks {
		if t.Typ != itemSpace && t.Typ != itemComment {
			sig = append(sig, t)
		}
	}
	// adjacent reports whether a and b touch, as in vars.$x.
	adjacent := func(a, b Item) bool { return a.Pos+len(a.Val) == b.Pos }
	namespace := func(i int) (string, core.Position, bool) {
		if i >= 2 && isPunct(sig[i-1], ".") && sig[i-2].Typ == itemIdent &&
			adjacent(sig[i-2], sig[i-1]) && adjacent(sig[i-1], sig[i]) {
			return sig[i-2].Val, p.posOf(sig[i-2]), true
		}
		return "", core.Position{}, false
	}
	for i, t := range sig {
		switch t.Typ {
		case itemVariable:
			ref := &Ref{Kind: SymVariable, Name: t.Val[1:], Range: p.rangeOf(t), Scope: scope}
			if ns, start, ok := namespace(i); ok {
				ref.Namespace, ref.Range.Start = ns, start
			}
			p.sheet.Refs = append(p.sheet.Refs, ref)
		case itemIdent:
			if i+1 >= len(sig) || !isPunct(sig[i+1], "(") || !adjacent(t, sig[i+1]) {
				continue
			}
			if strings.EqualFold(t.Val, "var") {
				if i+2 < len(sig) && sig[i+2].Typ == itemIdent && strings.HasPrefix(sig[i+2].Val, "--") {
					p.sheet.Refs = append(p.sheet.Refs, &Ref{
						Kind: SymCustomProperty, Name: sig[i+2].Val, Range: p.rangeOf(sig[i+2]), Scope: scope,
					})
				}
				continue
			}
			if !value || strings.EqualFold(t.Val, "url") {
				continue
			}
			ref := &Ref{Kind: SymFunction, Name: t.Val, Range: p.rangeOf(t), Scope: scope}
			if ns, start, ok := namespace(i); ok {
				ref.Namespace, ref.Range.Start = ns, start
			}
			p.sheet.Refs = append(p.sheet.Refs, ref)
		}
	}
}

// ---------------------------------------------------------------------------
// At-rules

func (p *parser) parseAtRule(parent *Node, ctx context, kw Item) {
	doc := p.takeDoc()
	name := strings.ToLower(kw.Val[1:])
	s := p.readStmt(p.nextNonSpaceOrTerm())
	params := s.toks
	paramText, paramOff := s.text(p)
	pr := p.rangeOfText(paramOff, paramOff+len(paramText))
	stmtRange := core.Range{Start: p.posOf(kw), End: pr.End}
	if isPunct(s.term, ";") {
		stmtRange.End = p.pos(p.off(s.term.Pos) + 1)
	}
	hasBlock := isPunct(s.term, "{")
	if isPunct(s.term, "}") {
		p.unread(s.term)
	}

	// block opens the at-rule's block; the node starts at the keyword.
	block := func(child context, setup func(*Node)) *Node {
		if !hasBlock {
			return nil
		}
		s2 := s
		n := p.openBlock(parent, s2, KindAtRule, name, paramText, pr, child.parents, child, func(n *Node) {
			n.Range.Start = p.posOf(kw)
			if setup != nil {
				setup(n)
			}
		})
		return n
	}

	switch name {
	case "use", "forward":
		p.parseUseForward(name, kw, params, stmtRange, ctx.scope)
		block(ctx, nil)

	case "import":
		for _, t := range params {
			if t.Typ == itemString {
				p.sheet.Imports = append(p.sheet.Imports, Import{
					Kind: ImportImport, URL: t.Val, URLRange: p.rangeOf(t), Range: stmtRange,
				})
			}
		}
		block(ctx, nil)

	case "mixin", "function":
		kind := SymMixin
		if name == "function" {
			kind = SymFunction
		}
		nameTok, paramsText, paramToks, _ := splitCallable(params)
		var sym *Symbol
		if nameTok.Typ == itemIdent {
			sym = &Symbol{
				Kind: kind, Name: nameTok.Val, Range: p.rangeOf(nameTok), Full: stmtRange,
				Scope: ctx.scope, Params: strings.TrimSpace(paramsText), Doc: doc,
			}
			p.sheet.Symbols = append(p.sheet.Symbols, sym)
		}
		n := block(context{scope: ctx.scope}, func(n *Node) {
			p.declareParams(paramToks, n)
		})
		if sym != nil && n != nil {
			sym.Full = n.Range
		}

	case "include":
		nameTok, _, argToks, after := splitCallable(params)
		if nameTok.Typ == itemIdent {
			ref := &Ref{Kind: SymMixin, Name: nameTok.Val, Range: p.rangeOf(nameTok), Scope: ctx.scope}
			if ns, nsTok, ok := leadingNamespace(params, nameTok); ok {
				ref.Namespace, ref.Range.Start = ns, p.posOf(nsTok)
			}
			p.sheet.Refs = append(p.sheet.Refs, ref)
		}
		using, _ := splitUsing(after)
		p.collectRefs(argToks, ctx.scope, true)
		block(ctx, func(n *Node) { p.declareParams(using, n) })

	case "extend":
		for _, t := range params {
			if t.Typ == itemPlaceholder {
				p.sheet.Refs = append(p.sheet.Refs, &Ref{
					Kind: SymPlaceholder, Name: t.Val[1:], Range: p.rangeOf(t), Scope: ctx.scope,
				})
			}
		}

	case "each", "for":
		var loopVars []Item
		for _, t := range params {
			if t.Typ == itemIdent && (t.Val == "in" || t.Val == "from") {
				break
			}
			if t.Typ == itemVariable {
				loopVars = append(loopVars, t)
			}
		}
		rest := params
		for i, t := range params {
			if t.Typ == itemIdent && (t.Val == "in" || t.Val == "from") {
				rest = params[i:]
				break
			}
		}
		p.collectRefs(rest, ctx.scope, true)
		block(ctx, func(n *Node) {
			for _, v := range loopVars {
				p.sheet.Symbols = append(p.sheet.Symbols, &Symbol{
					Kind: SymVariable, Name: v.Val[1:], Range: p.rangeOf(v), Full: p.rangeOf(v), Scope: n, Param: true,
				})
			}
		})

	case "keyframes", "-webkit-keyframes", "-moz-keyframes":
		var sym *Symbol
		for _, t := range params {
			if t.Typ == itemIdent || t.Typ == itemString {
				sym = &Symbol{Kind: SymKeyframes, Name: t.Val, Range: p.rangeOf(t), Full: stmtRange, Doc: doc}
				p.sheet.Symbols = append(p.sheet.Symbols, sym)
				break
			}
		}
		p.collectRefs(params, ctx.scope, false)
		n := block(context{scope: ctx.scope, keyframes: true}, nil)
		if sym != nil && n != nil {
			sym.Full = n.Range
		}

	case "at-root":
		if paramText != "" && !strings.HasPrefix(paramText, "(") && hasBlock {
			// @at-root .x { } is a style rule with no parent.
			s2 := s
			s2.toks = params
			p.parseStyleRule(parent, context{scope: ctx.scope}, s2)
			parent.Children[len(parent.Children)-1].Range.Start = p.posOf(kw)
			return
		}
		block(context{scope: ctx.scope}, nil)

	default:
		p.collectRefs(params, ctx.scope, true)
		block(ctx, nil)
	}
}

// nextNonSpaceOrTerm returns the first token after the at-keyword that is
// not a space (so readStmt sees the parameters' first token).
func (p *parser) nextNonSpaceOrTerm() Item {
	for {
		it := p.next()
		if it.Typ != itemSpace && it.Typ != itemComment {
			return it
		}
	}
}

func (p *parser) parseUseForward(name string, kw Item, params []Item, stmtRange core.Range, scope *Node) {
	imp := Import{Kind: ImportUse, Range: stmtRange}
	if name == "forward" {
		imp.Kind = ImportForward
	}
	sig := significant(params)
	i := 0
	if i < len(sig) && sig[i].Typ == itemString {
		imp.URL, imp.URLRange = sig[i].Val, p.rangeOf(sig[i])
		i++
	} else {
		p.errorf(p.posOf(kw), "%s needs a quoted URL", kw.Val)
		return
	}
	if imp.Kind == ImportUse {
		imp.Namespace = DefaultNamespace(imp.URL)
	}
	for ; i < len(sig); i++ {
		t := sig[i]
		if t.Typ != itemIdent {
			continue
		}
		switch t.Val {
		case "as":
			if i+1 >= len(sig) {
				break
			}
			n := sig[i+1]
			switch {
			case imp.Kind == ImportUse && isPunct(n, "*"):
				imp.Namespace = "*"
			case imp.Kind == ImportUse && n.Typ == itemIdent:
				imp.Namespace = n.Val
			case imp.Kind == ImportForward && n.Typ == itemIdent:
				imp.Prefix = n.Val // "prefix-" from "prefix-*"
			}
			i++
		case "show", "hide":
			var names []string
			for i+1 < len(sig) && (sig[i+1].Typ == itemIdent || sig[i+1].Typ == itemVariable || isPunct(sig[i+1], ",")) {
				i++
				if sig[i].Typ != itemPunct {
					names = append(names, sig[i].Val)
				}
			}
			if t.Val == "show" {
				imp.Show = names
			} else {
				imp.Hide = names
			}
		case "with":
			p.collectRefs(sig[i+1:], scope, true)
			i = len(sig)
		}
	}
	p.sheet.Imports = append(p.sheet.Imports, imp)
}

// declareParams declares the $variables of a parameter list in scope n and
// records references in their default values.
func (p *parser) declareParams(toks []Item, n *Node) {
	sig := significant(toks)
	depth := 0
	expectName := true
	for i, t := range sig {
		switch {
		case isPunct(t, "(") || isPunct(t, "["):
			depth++
		case isPunct(t, ")") || isPunct(t, "]"):
			depth--
		case isPunct(t, ",") && depth == 0:
			expectName = true
			continue
		case t.Typ == itemVariable && expectName && depth == 0:
			sym := &Symbol{Kind: SymVariable, Name: t.Val[1:], Range: p.rangeOf(t), Full: p.rangeOf(t), Scope: n, Param: true}
			if i+1 < len(sig) && isPunct(sig[i+1], ":") {
				end := i + 2
				for end < len(sig) && !(isPunct(sig[end], ",") && depth == 0) {
					end++
				}
				if end > i+2 {
					sym.Value = strings.TrimSpace(p.input[p.off(sig[i+2].Pos) : p.off(sig[end-1].Pos)+len(sig[end-1].Val)])
				}
			}
			p.sheet.Symbols = append(p.sheet.Symbols, sym)
			expectName = false
			continue
		case t.Typ == itemVariable:
			p.sheet.Refs = append(p.sheet.Refs, &Ref{Kind: SymVariable, Name: t.Val[1:], Range: p.rangeOf(t), Scope: n})
		}
		expectName = false
	}
}

// splitCallable splits "name(args) after" into the name token, the text and
// tokens of args, and the tokens after the closing parenthesis. Without
// parentheses, args is empty and after holds everything after the name.
func splitCallable(params []Item) (name Item, args string, argToks, after []Item) {
	sig := significant(params)
	idx := -1
	for i, t := range sig {
		if t.Typ == itemIdent {
			idx = i
			// Skip a namespace: ns.name
			if i+2 < len(sig) && isPunct(sig[i+1], ".") && sig[i+2].Typ == itemIdent {
				continue
			}
			break
		}
	}
	if idx < 0 {
		return Item{}, "", nil, nil
	}
	name = sig[idx]
	rest := sig[idx+1:]
	if len(rest) > 0 && isPunct(rest[0], "(") {
		depth := 0
		var b strings.Builder
		for i, t := range rest {
			if isPunct(t, "(") {
				depth++
				if depth == 1 {
					continue
				}
			}
			if isPunct(t, ")") {
				depth--
				if depth == 0 {
					return name, b.String(), rest[1:i], rest[i+1:]
				}
			}
			if b.Len() > 0 && i > 0 && rest[i-1].Pos+len(rest[i-1].Val) != t.Pos {
				b.WriteByte(' ')
			}
			b.WriteString(quoteIfString(t))
		}
		return name, b.String(), rest[1:], nil
	}
	return name, "", nil, rest
}

func quoteIfString(t Item) string {
	if t.Typ == itemString {
		return `"` + t.Val + `"`
	}
	return t.Val
}

// leadingNamespace returns ns for "ns.name".
func leadingNamespace(params []Item, nameTok Item) (string, Item, bool) {
	sig := significant(params)
	for i := 2; i < len(sig); i++ {
		if sig[i] == nameTok && isPunct(sig[i-1], ".") && sig[i-2].Typ == itemIdent {
			return sig[i-2].Val, sig[i-2], true
		}
	}
	return "", Item{}, false
}

// splitUsing splits the tokens after @include's arguments at "using",
// returning the parameter list of the using clause.
func splitUsing(toks []Item) (using, rest []Item) {
	for i, t := range toks {
		if t.Typ == itemIdent && t.Val == "using" {
			using = significant(toks[i+1:])
			if len(using) >= 2 && isPunct(using[0], "(") && isPunct(using[len(using)-1], ")") {
				using = using[1 : len(using)-1]
			}
			return using, toks[:i]
		}
	}
	return nil, toks
}

func significant(toks []Item) []Item {
	out := make([]Item, 0, len(toks))
	for _, t := range toks {
		if t.Typ != itemSpace && t.Typ != itemComment {
			out = append(out, t)
		}
	}
	return out
}

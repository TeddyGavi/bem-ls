package core

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Position is a location in the source. Line and Col are 1-based, and Col
// counts bytes.
type Position struct {
	Offset int
	Line   int
	Col    int
}

// Range is a half-open source range [Start, End).
type Range struct {
	Start Position
	End   Position
}

// ClassKind says how a class name was found in a className expression.
type ClassKind int

const (
	// ClassStatic is always applied: className="a" or the plain parts of a
	// template literal.
	ClassStatic ClassKind = iota
	// ClassConditional is applied depending on runtime state: ternary
	// branches, && operands and object keys in clsx(...).
	ClassConditional
	// ClassModule is a CSS-modules reference such as styles.button.
	ClassModule
	// ClassDynamic is only partly known, e.g. `btn--${size}`. Name holds
	// the known part.
	ClassDynamic
)

func (k ClassKind) String() string {
	switch k {
	case ClassStatic:
		return "static"
	case ClassConditional:
		return "conditional"
	case ClassModule:
		return "module"
	case ClassDynamic:
		return "dynamic"
	}
	return fmt.Sprintf("ClassKind(%d)", int(k))
}

// ClassRef is one class name used by an element.
type ClassRef struct {
	Name  string
	Range Range
	Kind  ClassKind
}

// Node is a JSX element. The tree returned by Parser.Root has a virtual
// root (Parent == nil) whose children are the top-level elements.
type Node struct {
	Tag         string // "" for fragments and the root
	Classes     []ClassRef
	Range       Range // from '<' to the end of the closing tag or "/>"
	SelfClosing bool
	Children    []*Node
	Parent      *Node
}

// IsRoot reports whether n is the virtual root.
func (n *Node) IsRoot() bool { return n.Parent == nil }

// IsFragment reports whether n is <>...</>.
func (n *Node) IsFragment() bool { return n.Parent != nil && n.Tag == "" }

// IsComponent reports whether n is a component (<Card>, <ui.Card>) rather
// than an intrinsic element (<div>).
func (n *Node) IsComponent() bool {
	if n.Tag == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(n.Tag)
	return unicode.IsUpper(r) || strings.ContainsRune(n.Tag, '.')
}

// Error is a lexing or parsing problem.
type Error struct {
	Pos Position
	Msg string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%d:%d: %s", e.Pos.Line, e.Pos.Col, e.Msg)
}

// ErrorList is the error returned by Parse when there are problems.
type ErrorList []*Error

func (l ErrorList) Error() string {
	msgs := make([]string, len(l))
	for i, e := range l {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "\n")
}

// Parser builds a Node tree from lexer items.
type Parser struct {
	// ClassAttrs are the attributes that hold class names.
	ClassAttrs []string
	// ClassFuncs are helpers whose arguments are class names, like clsx.
	ClassFuncs []string
	// ModuleIdents are the identifiers that refer to an imported CSS module.
	ModuleIdents []string

	tokens  <-chan Item
	root    *Node
	current *Node
	stack   []*Node // stack[0] is root
	peeked  []Item
	errs    ErrorList
}

// NewParser returns a parser reading from tokens, as returned by Lex.
func NewParser(tokens <-chan Item) *Parser {
	root := &Node{}
	return &Parser{
		ClassAttrs:   []string{"className", "class"},
		ClassFuncs:   []string{"clsx", "classnames", "classNames", "cx", "cn", "twMerge", "twJoin"},
		ModuleIdents: []string{"styles", "classes"},
		tokens:       tokens,
		root:         root,
		current:      root,
		stack:        []*Node{root},
	}
}

// ParseString lexes and parses src. The tree is returned even when err is
// not nil, so callers can use partial results.
func ParseString(name, src string) (*Node, error) {
	_, tokens := Lex(name, src)
	p := NewParser(tokens)
	err := p.Parse()
	return p.Root(), err
}

// Root returns the virtual root of the parsed tree.
func (p *Parser) Root() *Node { return p.root }

// Parse reads every token and builds the tree. It returns an ErrorList if
// the lexer or the parser found problems.
func (p *Parser) Parse() error {
	for {
		it := p.next()
		switch it.Typ {
		case itemEOF:
			p.finish(it)
			if len(p.errs) > 0 {
				return p.errs
			}
			return nil
		case itemTagOpen:
			p.parseElement(it)
		case itemTagCloseOpen:
			p.parseClosingTag(it)
		}
	}
}

func (p *Parser) parseElement(open Item) {
	n := &Node{Range: Range{Start: startPos(open)}}
	if name := p.next(); name.Typ == itemTagName {
		n.Tag = name.Val
	} else {
		p.unread(name)
	}
	p.push(n)
	for {
		it := p.next()
		switch it.Typ {
		case itemAttrName:
			p.parseAttr(n, it)
		case itemOpenBrace: // spread
			p.parseExpr(false)
		case itemTagClose:
			return
		case itemTagSelfClose:
			n.SelfClosing = true
			n.Range.End = endPos(it)
			p.pop()
			return
		case itemEOF:
			p.unread(it)
			return
		}
	}
}

func (p *Parser) parseAttr(n *Node, name Item) {
	if p.peek().Typ != itemEqual {
		return // boolean attribute
	}
	p.next()
	isClass := contains(p.ClassAttrs, name.Val)
	switch v := p.next(); v.Typ {
	case itemRawString:
		if isClass {
			n.Classes = append(n.Classes, wordsOf(v, 0, len(v.Val), ClassStatic, false, false)...)
		}
	case itemOpenBrace:
		toks := p.parseExpr(isClass)
		if isClass {
			n.Classes = append(n.Classes, p.extractClasses(toks)...)
		}
	case itemTagOpen:
		p.parseElement(v)
	default:
		p.unread(v)
	}
}

// parseExpr consumes tokens up to the '}' matching an already consumed '{'.
// If collect is true it returns the tokens in between; otherwise it builds
// nodes for any JSX inside, e.g. icon={<Icon />}.
func (p *Parser) parseExpr(collect bool) []Item {
	var toks []Item
	depth := 0
	for {
		it := p.next()
		switch it.Typ {
		case itemEOF:
			p.unread(it)
			return toks
		case itemOpenBrace:
			depth++
		case itemCloseBrace:
			if depth == 0 {
				return toks
			}
			depth--
		case itemTagOpen:
			if !collect {
				p.parseElement(it)
				continue
			}
		case itemTagCloseOpen:
			if !collect {
				p.parseClosingTag(it)
				continue
			}
		}
		if collect {
			toks = append(toks, it)
		}
	}
}

func (p *Parser) parseClosingTag(open Item) {
	name := p.next()
	if name.Typ != itemTagCloseName {
		p.unread(name)
		name = Item{Pos: open.Pos + len(open.Val), Line: open.Line, Col: open.Col + len(open.Val)}
	}
	end := endPos(name)
	if p.peek().Typ == itemTagClose {
		end = endPos(p.next())
	}
	start := startPos(open)
	for i := len(p.stack) - 1; i > 0; i-- {
		if p.stack[i].Tag != name.Val {
			continue
		}
		for j := len(p.stack) - 1; j > i; j-- {
			p.unclosed(p.stack[j], start)
		}
		p.stack[i].Range.End = end
		p.stack = p.stack[:i]
		p.current = p.stack[i-1]
		return
	}
	p.errorf(start, "unexpected closing tag </%s>", name.Val)
}

func (p *Parser) finish(eofItem Item) {
	end := startPos(eofItem)
	for j := len(p.stack) - 1; j > 0; j-- {
		p.unclosed(p.stack[j], end)
	}
	p.stack = p.stack[:1]
	p.current = p.root
}

func (p *Parser) unclosed(n *Node, end Position) {
	p.errorf(n.Range.Start, "%s is not closed", tagLabel(n.Tag))
	n.Range.End = end
}

func tagLabel(tag string) string {
	if tag == "" {
		return "fragment <>"
	}
	return "<" + tag + ">"
}

func (p *Parser) push(n *Node) {
	n.Parent = p.current
	p.current.Children = append(p.current.Children, n)
	p.stack = append(p.stack, n)
	p.current = n
}

func (p *Parser) pop() {
	if len(p.stack) > 1 {
		p.stack = p.stack[:len(p.stack)-1]
		p.current = p.stack[len(p.stack)-1]
	}
}

// next returns the next token, recording and skipping itemError tokens. It
// returns itemEOF forever once the channel is closed.
func (p *Parser) next() Item {
	if n := len(p.peeked); n > 0 {
		it := p.peeked[n-1]
		p.peeked = p.peeked[:n-1]
		return it
	}
	for it := range p.tokens {
		if it.Typ == itemError {
			p.errs = append(p.errs, &Error{Pos: startPos(it), Msg: it.Val})
			continue
		}
		return it
	}
	return Item{Typ: itemEOF}
}

func (p *Parser) peek() Item {
	it := p.next()
	p.unread(it)
	return it
}

func (p *Parser) unread(it Item) { p.peeked = append(p.peeked, it) }

func (p *Parser) errorf(pos Position, format string, args ...any) {
	p.errs = append(p.errs, &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// ---------------------------------------------------------------------------
// Class extraction from className={...}

// exFrame is a bracket level inside a className expression.
type exFrame struct {
	kind     byte // '(' '[' '{' '`' '$' or 0 for the top level
	skip     bool // strings here are not class names, e.g. t('key')
	seg      int  // current comma-separated segment
	objValue bool // in an object, after the key's ':'
	ternary  int  // unmatched '?' in the current segment
}

// segment is a comma-separated part of a bracket level. A string is
// conditional if its segment, or any enclosing one, contains ?, &&, || or ??.
type segment struct {
	parent int
	cond   bool
}

type classCandidate struct {
	tok        Item
	seg        int
	forceCond  bool
	dynFirst   bool // first word is glued to an interpolation
	dynLast    bool // last word is glued to an interpolation
	isTemplate bool
}

// extractClasses finds the class names in the tokens of a className
// expression (without the outer braces).
func (p *Parser) extractClasses(toks []Item) []ClassRef {
	var (
		refs   []ClassRef
		cands  []classCandidate
		segs   = []segment{{parent: -1}}
		frames = []exFrame{{}}
	)
	top := func() *exFrame { return &frames[len(frames)-1] }
	tok := func(i int) Item {
		if i < 0 || i >= len(toks) {
			return Item{Typ: itemEOF}
		}
		return toks[i]
	}
	isPunct := func(it Item, vals ...string) bool {
		return it.Typ == itemPunct && contains(vals, it.Val)
	}
	open := func(kind byte, skip bool) {
		segs = append(segs, segment{parent: top().seg})
		frames = append(frames, exFrame{kind: kind, skip: skip, seg: len(segs) - 1})
	}
	closeFrame := func() {
		if len(frames) > 1 {
			frames = frames[:len(frames)-1]
		}
	}
	comparison := []string{"===", "!==", "==", "!="}

	for i := 0; i < len(toks); i++ {
		it := toks[i]
		f := top()
		switch it.Typ {
		case itemPunct:
			switch it.Val {
			case "(":
				prev := tok(i - 1)
				switch {
				case prev.Typ == itemIdent && contains(p.ClassFuncs, prev.Val):
					open('(', false)
				case prev.Typ == itemIdent && !exprKeywords[prev.Val],
					isPunct(prev, ")", "]"), prev.Typ == itemBracketString:
					open('(', true) // call of an unrelated function
				default:
					open('(', f.skip) // grouping
				}
			case "[":
				prev := tok(i - 1)
				member := (prev.Typ == itemIdent && !exprKeywords[prev.Val]) || isPunct(prev, ")", "]")
				open('[', f.skip || member)
			case ")", "]":
				closeFrame()
			case ",":
				segs = append(segs, segment{parent: segs[f.seg].parent})
				f.seg = len(segs) - 1
				f.objValue = false
				f.ternary = 0
			case "?":
				segs[f.seg].cond = true
				f.ternary++
			case "&&", "||", "??":
				segs[f.seg].cond = true
			case ":":
				if f.ternary > 0 {
					f.ternary--
				} else if f.kind == '{' {
					f.objValue = true
				}
			}
		case itemOpenBrace:
			open('{', f.skip)
		case itemCloseBrace:
			closeFrame()
		case itemTemplateOpen:
			frames = append(frames, exFrame{kind: '`', skip: f.skip || (f.kind == '{' && f.objValue), seg: f.seg})
		case itemTemplateClose:
			closeFrame()
		case itemTemplateExprOpen:
			open('$', f.skip)
		case itemTemplateExprClose:
			closeFrame()
		case itemTemplatePart:
			if f.skip {
				continue
			}
			cands = append(cands, classCandidate{
				tok:        it,
				seg:        f.seg,
				dynFirst:   tok(i-1).Typ == itemTemplateExprClose && !startsWithSpace(it.Val),
				dynLast:    tok(i+1).Typ == itemTemplateExprOpen && !endsWithSpace(it.Val),
				isTemplate: true,
			})
		case itemString:
			if f.skip || (f.kind == '{' && f.objValue) ||
				isPunct(tok(i-1), comparison...) || isPunct(tok(i+1), comparison...) {
				continue
			}
			cands = append(cands, classCandidate{
				tok:       it,
				seg:       f.seg,
				forceCond: f.kind == '{' && isPunct(tok(i+1), ":"),
				dynFirst:  isPunct(tok(i-1), "+") && !startsWithSpace(it.Val),
				dynLast:   isPunct(tok(i+1), "+") && !endsWithSpace(it.Val),
			})
		case itemIdent:
			next := tok(i + 1)
			switch {
			case contains(p.ModuleIdents, it.Val) && next.Typ == itemDot && tok(i+2).Typ == itemIdent:
				refs = append(refs, ClassRef{Name: tok(i + 2).Val, Range: rangeOf(tok(i + 2)), Kind: ClassModule})
				i += 2
			case contains(p.ModuleIdents, it.Val) && next.Typ == itemBracketString:
				refs = append(refs, ClassRef{Name: next.Val, Range: rangeOf(next), Kind: ClassModule})
				i++
			case f.kind == '{' && !f.skip && !f.objValue && isPunct(next, ":") && tok(i-1).Typ != itemDot:
				refs = append(refs, ClassRef{Name: it.Val, Range: rangeOf(it), Kind: ClassConditional})
			}
		}
	}

	condOf := func(s int) bool {
		for ; s >= 0; s = segs[s].parent {
			if segs[s].cond {
				return true
			}
		}
		return false
	}
	for _, c := range cands {
		kind := ClassStatic
		if c.forceCond || condOf(c.seg) {
			kind = ClassConditional
		}
		refs = append(refs, wordsOf(c.tok, 0, len(c.tok.Val), kind, c.dynFirst, c.dynLast)...)
	}
	sortRefs(refs)
	return refs
}

// wordsOf splits it.Val[from:to] on whitespace into ClassRefs. Words glued
// to an interpolation at either end are marked ClassDynamic.
func wordsOf(it Item, from, to int, kind ClassKind, dynFirst, dynLast bool) []ClassRef {
	s := it.Val[from:to]
	var refs []ClassRef
	i := 0
	for i < len(s) {
		for i < len(s) && isSpace(rune(s[i])) {
			i++
		}
		j := i
		for j < len(s) && !isSpace(rune(s[j])) {
			j++
		}
		if j > i {
			k := kind
			if (i == 0 && dynFirst) || (j == len(s) && dynLast) {
				k = ClassDynamic
			}
			refs = append(refs, ClassRef{
				Name:  s[i:j],
				Range: Range{Start: posWithin(it, from+i), End: posWithin(it, from+j)},
				Kind:  k,
			})
		}
		i = j
	}
	return refs
}

func sortRefs(refs []ClassRef) {
	// Insertion sort by offset: lists are short and mostly sorted.
	for i := 1; i < len(refs); i++ {
		for j := i; j > 0 && refs[j].Range.Start.Offset < refs[j-1].Range.Start.Offset; j-- {
			refs[j], refs[j-1] = refs[j-1], refs[j]
		}
	}
}

// ---------------------------------------------------------------------------
// Positions

func startPos(it Item) Position {
	return Position{Offset: it.Pos, Line: it.Line, Col: it.Col}
}

func endPos(it Item) Position { return posWithin(it, len(it.Val)) }

// posWithin returns the position of byte k of it.Val.
func posWithin(it Item, k int) Position {
	p := startPos(it)
	s := it.Val[:k]
	p.Offset += k
	if n := strings.Count(s, "\n"); n > 0 {
		p.Line += n
		p.Col = k - strings.LastIndexByte(s, '\n')
	} else {
		p.Col += k
	}
	return p
}

func rangeOf(it Item) Range { return Range{Start: startPos(it), End: endPos(it)} }

func startsWithSpace(s string) bool { return s != "" && isSpace(rune(s[0])) }

func endsWithSpace(s string) bool { return s != "" && isSpace(rune(s[len(s)-1])) }

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

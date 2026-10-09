package core

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// ItemType identifies the type of a lexical item.
type ItemType int

// Item is a token emitted by the lexer.
//
// For every item except itemError, Val is exactly input[Pos:Pos+len(Val)],
// so source ranges can be derived from Pos and Val. Quoted values
// (itemRawString, itemString, itemBracketString) exclude the quotes.
// For itemError, Val holds the message and Pos the offending location.
type Item struct {
	Typ  ItemType
	Val  string
	Pos  int // byte offset in the input
	Line int // 1-based line of Pos
	Col  int // 1-based byte column of Pos
}

const (
	itemError ItemType = iota // lexing problem; Val is the message
	itemEOF

	// JSX structure.
	itemTagOpen      // <
	itemTagName      // div, Foo.Bar; empty for a fragment <>
	itemTagClose     // > ending an opening or closing tag
	itemTagSelfClose // />
	itemTagCloseOpen // </
	itemTagCloseName // name in </name>; empty for </>
	itemAttrName     // className
	itemEqual        // = after an attribute name
	itemRawString    // quoted attribute value, without quotes
	itemText         // non-blank JSX child text
	itemOpenBrace    // {
	itemCloseBrace   // }

	// JavaScript inside {...} and outside JSX.
	itemIdent             // identifier or keyword
	itemNumber            // numeric literal
	itemString            // '...' or "..." without quotes
	itemRegex             // /re/flags
	itemDot               // . (member access)
	itemBracketString     // x in obj['x'], without quotes
	itemTemplateOpen      // `
	itemTemplatePart      // raw text between template delimiters
	itemTemplateExprOpen  // ${
	itemTemplateExprClose // } closing ${
	itemTemplateClose     // `
	itemPunct             // any other operator or punctuation
)

const eof = -1

// tokenBuffer is the capacity of the token channel. It lets the lexer run
// ahead of the parser instead of switching goroutines on every token.
const tokenBuffer = 64

var itemTypeNames = map[ItemType]string{
	itemError:             "itemError",
	itemEOF:               "itemEOF",
	itemTagOpen:           "itemTagOpen",
	itemTagName:           "itemTagName",
	itemTagClose:          "itemTagClose",
	itemTagSelfClose:      "itemTagSelfClose",
	itemTagCloseOpen:      "itemTagCloseOpen",
	itemTagCloseName:      "itemTagCloseName",
	itemAttrName:          "itemAttrName",
	itemEqual:             "itemEqual",
	itemRawString:         "itemRawString",
	itemText:              "itemText",
	itemOpenBrace:         "itemOpenBrace",
	itemCloseBrace:        "itemCloseBrace",
	itemIdent:             "itemIdent",
	itemNumber:            "itemNumber",
	itemString:            "itemString",
	itemRegex:             "itemRegex",
	itemDot:               "itemDot",
	itemBracketString:     "itemBracketString",
	itemTemplateOpen:      "itemTemplateOpen",
	itemTemplatePart:      "itemTemplatePart",
	itemTemplateExprOpen:  "itemTemplateExprOpen",
	itemTemplateExprClose: "itemTemplateExprClose",
	itemTemplateClose:     "itemTemplateClose",
	itemPunct:             "itemPunct",
}

func (t ItemType) String() string {
	if name, ok := itemTypeNames[t]; ok {
		return name
	}
	return fmt.Sprintf("ItemType(%d)", int(t))
}

func (i Item) String() string {
	return fmt.Sprintf("%d:%d %s %q", i.Line, i.Col, i.Typ, i.Val)
}

// frameKind is the lexical context the lexer is in. Contexts nest: JSX
// contains {expressions}, which contain JSX or `templates`, and so on.
type frameKind uint8

const (
	frameCode     frameKind = iota // JavaScript
	frameTemplate                  // inside a template literal
	frameTag                       // between <name and > or />
	frameChildren                  // between <tag> and </tag>
)

type frame struct {
	kind  frameKind
	depth int    // unmatched { inside a frameCode
	pos   int    // where the frame started, for error messages
	tag   string // element name for frameTag and frameChildren
}

// Lexer turns JSX/TSX source into Items. It runs as a state machine in its
// own goroutine and sends Items on a channel.
type Lexer struct {
	name       string
	input      string
	start      int // start of the pending item
	pos        int // current position
	width      int // width of the last rune read by next
	tokens     chan Item
	done       chan struct{}
	stopOnce   sync.Once
	stopped    bool    // only touched by the lexer goroutine
	frames     []frame // context stack; frames[0] is top-level code
	prev       Item    // last emitted non-error item
	hasPrev    bool
	lineStarts []int
}

// StateFunc is a lexer state. It returns the next state, or nil when done.
type StateFunc func(*Lexer) StateFunc

// Lex starts lexing input in a new goroutine. The returned channel is closed
// after itemEOF. A consumer that stops reading early must call Stop so the
// goroutine can exit.
func Lex(name, input string) (*Lexer, <-chan Item) {
	l := &Lexer{
		name:   name,
		input:  input,
		tokens: make(chan Item, tokenBuffer),
		done:   make(chan struct{}),
		frames: []frame{{kind: frameCode}},
	}
	go l.run()
	return l, l.tokens
}

// Name returns the name given to Lex, usually a file name.
func (l *Lexer) Name() string { return l.name }

// Stop makes the lexer goroutine finish early. The token channel is still
// closed. It is safe to call Stop more than once and after lexing finished.
func (l *Lexer) Stop() {
	l.stopOnce.Do(func() { close(l.done) })
}

func (l *Lexer) run() {
	defer close(l.tokens)
	l.lineStarts = append(l.lineStarts, 0)
	for i := 0; i < len(l.input); i++ {
		if l.input[i] == '\n' {
			l.lineStarts = append(l.lineStarts, i+1)
		}
	}
	for state := lexCode; state != nil && !l.stopped; {
		state = state(l)
	}
}

// ---------------------------------------------------------------------------
// States

// lexCode lexes JavaScript: top-level code and the inside of {expressions}
// and ${template expressions}.
func lexCode(l *Lexer) StateFunc {
	for !l.stopped {
		r := l.peek()
		switch {
		case r == eof:
			return lexEOF
		case isSpace(r):
			l.next()
			l.ignore()
		case r == '/' && l.byteAt(1) == '/':
			l.skipLineComment()
		case r == '/' && l.byteAt(1) == '*':
			l.skipBlockComment()
		case r == '"' || r == '\'':
			l.lexString()
		case r == '`':
			l.push(frameTemplate)
			l.next()
			l.emit(itemTemplateOpen)
			return lexTemplate
		case r == '{':
			l.next()
			l.emit(itemOpenBrace)
			l.top().depth++
		case r == '}':
			l.next()
			if t := l.top(); t.depth > 0 || len(l.frames) == 1 {
				if t.depth > 0 {
					t.depth--
				}
				l.emit(itemCloseBrace)
				continue
			}
			l.pop()
			if l.top().kind == frameTemplate {
				l.emit(itemTemplateExprClose)
			} else {
				l.emit(itemCloseBrace)
			}
			return l.resume()
		case r == '<' && l.exprAllowed() && l.jsxTagAhead():
			return lexTagOpen
		case r == '/' && l.exprAllowed():
			l.lexRegex()
		case isIdentStart(r):
			l.acceptRun(isIdentPart)
			l.emit(itemIdent)
		case isDigit(r) || (r == '.' && isDigit(rune(l.byteAt(1)))):
			l.next()
			l.acceptRun(isNumberPart)
			l.emit(itemNumber)
		case r == '.' && !strings.HasPrefix(l.rest(), "..."):
			l.next()
			l.emit(itemDot)
		case r == '[' && l.lexBracketString():
		default:
			l.lexPunct()
		}
	}
	return nil
}

// lexTemplate lexes the body of a template literal.
func lexTemplate(l *Lexer) StateFunc {
	for {
		switch r := l.next(); {
		case r == eof:
			l.emitNonEmpty(itemTemplatePart)
			return lexEOF
		case r == '\\':
			l.next()
		case r == '`':
			l.backup()
			l.emitNonEmpty(itemTemplatePart)
			l.next()
			l.emit(itemTemplateClose)
			l.pop()
			return l.resume()
		case r == '$' && l.peek() == '{':
			l.backup()
			l.emitNonEmpty(itemTemplatePart)
			l.push(frameCode)
			l.pos += 2
			l.emit(itemTemplateExprOpen)
			return lexCode
		}
	}
}

// lexChildren lexes JSX children: text, {expressions} and nested tags.
func lexChildren(l *Lexer) StateFunc {
	for !l.stopped {
		switch r := l.peek(); {
		case r == eof:
			l.emitText()
			return lexEOF
		case r == '<' && (l.byteAt(1) == '/' || l.jsxTagAhead()):
			l.emitText()
			return lexTagOpen
		case r == '{':
			l.emitText()
			l.push(frameCode)
			l.next()
			l.emit(itemOpenBrace)
			return lexCode
		default:
			l.next()
		}
	}
	return nil
}

// lexTagOpen lexes "<name" or a whole closing tag "</name>". It is entered
// with l.pos at '<'.
func lexTagOpen(l *Lexer) StateFunc {
	if strings.HasPrefix(l.rest(), "</") {
		l.pos += 2
		l.emit(itemTagCloseOpen)
		l.skipSpace()
		l.acceptRun(isTagNamePart)
		name := l.input[l.start:l.pos]
		l.emit(itemTagCloseName)
		l.skipSpace()
		if l.peek() == '>' {
			l.next()
			l.emit(itemTagClose)
		} else {
			l.errorf(l.pos, "expected '>' to end closing tag")
		}
		l.closeChildren(name)
		return l.resume()
	}
	l.push(frameTag)
	l.next()
	l.emit(itemTagOpen)
	l.skipSpace()
	l.acceptRun(isTagNamePart)
	l.top().tag = l.input[l.start:l.pos]
	l.emit(itemTagName)
	return lexAttributes
}

// lexAttributes lexes the inside of an opening tag after its name.
func lexAttributes(l *Lexer) StateFunc {
	for !l.stopped {
		l.skipSpace()
		switch r := l.peek(); {
		case r == eof:
			return lexEOF
		case r == '>':
			l.next()
			l.emit(itemTagClose)
			l.top().kind = frameChildren
			return lexChildren
		case r == '/' && l.byteAt(1) == '>':
			l.pos += 2
			l.emit(itemTagSelfClose)
			l.pop()
			return l.resume()
		case r == '/' && l.byteAt(1) == '/':
			l.skipLineComment()
		case r == '/' && l.byteAt(1) == '*':
			l.skipBlockComment()
		case r == '{': // spread: {...props}
			l.push(frameCode)
			l.next()
			l.emit(itemOpenBrace)
			return lexCode
		case isAttrNameStart(r):
			return lexAttrName
		default:
			l.next()
			l.errorf(l.start, "unexpected %q in tag", r)
			l.ignore()
		}
	}
	return nil
}

// lexAttrName lexes an attribute name and, if present, its '='.
func lexAttrName(l *Lexer) StateFunc {
	l.acceptRun(isAttrNamePart)
	l.emit(itemAttrName)
	l.skipSpace()
	if l.peek() != '=' {
		return lexAttributes // boolean attribute
	}
	l.next()
	l.emit(itemEqual)
	return lexAttrValue
}

// lexAttrValue lexes the value after "name=".
func lexAttrValue(l *Lexer) StateFunc {
	l.skipSpace()
	switch r := l.peek(); {
	case r == '"' || r == '\'':
		open := l.pos
		l.next()
		l.ignore()
		i := strings.IndexRune(l.rest(), r)
		if i < 0 {
			l.pos = len(l.input)
			l.emit(itemRawString)
			l.errorf(open, "unterminated attribute value")
			return lexEOF
		}
		l.pos += i
		l.emit(itemRawString)
		l.next()
		l.ignore()
		return lexAttributes
	case r == '{':
		l.push(frameCode)
		l.next()
		l.emit(itemOpenBrace)
		return lexCode
	case r == '<' && l.jsxTagAhead():
		return lexTagOpen
	default:
		l.errorf(l.pos, "expected attribute value")
		return lexAttributes
	}
}

// lexEOF reports unclosed contexts and emits itemEOF.
func lexEOF(l *Lexer) StateFunc {
	for i := len(l.frames) - 1; i > 0; i-- {
		switch f := l.frames[i]; f.kind {
		case frameTag:
			l.errorf(f.pos, "unterminated tag")
		case frameCode:
			l.errorf(f.pos, "unterminated expression: missing '}'")
		case frameTemplate:
			l.errorf(f.pos, "unterminated template literal")
		case frameChildren:
			// Reported by the parser, which knows the tag name.
		}
	}
	l.frames = l.frames[:1]
	l.ignore()
	l.emit(itemEOF)
	return nil
}

// ---------------------------------------------------------------------------
// Sub-lexers used by lexCode. Each one consumes at least one rune.

func (l *Lexer) lexString() {
	quote := l.next()
	open := l.pos - 1
	l.ignore()
	for {
		switch r := l.next(); r {
		case '\\':
			l.next()
		case quote:
			l.backup()
			l.emit(itemString)
			l.next()
			l.ignore()
			return
		case '\n', eof:
			l.backup()
			l.emit(itemString)
			l.errorf(open, "unterminated string literal")
			return
		}
	}
}

func (l *Lexer) lexRegex() {
	l.next() // opening '/'
	inClass := false
	for {
		switch r := l.next(); {
		case r == eof:
			l.emit(itemRegex)
			return
		case r == '\n':
			l.backup()
			l.emit(itemRegex)
			return
		case r == '\\':
			l.next()
		case r == '[':
			inClass = true
		case r == ']':
			inClass = false
		case r == '/' && !inClass:
			l.acceptRun(isIdentPart) // flags
			l.emit(itemRegex)
			return
		}
	}
}

// lexBracketString recognises obj['key'] and emits key as itemBracketString.
// It reports false, consuming nothing, if the input doesn't have that shape.
func (l *Lexer) lexBracketString() bool {
	if !l.memberAllowed() {
		return false
	}
	in := l.input
	i := skipSpaceAt(in, l.pos+1)
	if i >= len(in) || (in[i] != '\'' && in[i] != '"') {
		return false
	}
	quote := in[i]
	i++
	contentStart := i
	for i < len(in) && in[i] != quote {
		if in[i] == '\\' || in[i] == '\n' {
			return false
		}
		i++
	}
	if i >= len(in) {
		return false
	}
	contentEnd := i
	i = skipSpaceAt(in, i+1)
	if i >= len(in) || in[i] != ']' {
		return false
	}
	l.start, l.pos = contentStart, contentEnd
	l.emit(itemBracketString)
	l.pos = i + 1
	l.ignore()
	return true
}

// operators is ordered longest first for maximal munch.
var operators = []string{
	">>>=",
	"...", "===", "!==", "**=", "<<=", ">>=", ">>>", "&&=", "||=", "??=",
	"=>", "==", "!=", "<=", ">=", "&&", "||", "??", "?.", "++", "--",
	"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "<<", ">>", "**",
}

func (l *Lexer) lexPunct() {
	rest := l.rest()
	for _, op := range operators {
		if strings.HasPrefix(rest, op) {
			l.pos += len(op)
			l.emit(itemPunct)
			return
		}
	}
	l.next()
	l.emit(itemPunct)
}

func (l *Lexer) skipLineComment() {
	if i := strings.IndexByte(l.rest(), '\n'); i >= 0 {
		l.pos += i
	} else {
		l.pos = len(l.input)
	}
	l.ignore()
}

func (l *Lexer) skipBlockComment() {
	open := l.pos
	if i := strings.Index(l.input[l.pos+2:], "*/"); i >= 0 {
		l.pos += 2 + i + 2
	} else {
		l.pos = len(l.input)
		l.errorf(open, "unterminated comment")
	}
	l.ignore()
}

// ---------------------------------------------------------------------------
// Context decisions

var exprKeywords = map[string]bool{
	"return": true, "yield": true, "await": true, "case": true,
	"default": true, "throw": true, "in": true, "of": true, "typeof": true,
	"void": true, "delete": true, "instanceof": true, "do": true, "else": true,
}

// exprAllowed reports whether an expression may start here, judging by the
// previous significant token. It separates JSX "<" from "less than" and a
// regex "/" from division.
func (l *Lexer) exprAllowed() bool {
	if !l.hasPrev {
		return true
	}
	switch p := l.prev; p.Typ {
	case itemPunct:
		return p.Val != ")" && p.Val != "]" && p.Val != "++" && p.Val != "--"
	case itemOpenBrace, itemCloseBrace, itemTemplateExprOpen:
		return true
	case itemTagClose, itemTagSelfClose:
		// After a complete element only another element makes sense. This
		// accepts adjacent top-level elements, as in pasted markup.
		return true
	case itemIdent:
		return exprKeywords[p.Val]
	}
	return false
}

// memberAllowed reports whether a '[' here is a member access.
func (l *Lexer) memberAllowed() bool {
	if !l.hasPrev {
		return false
	}
	switch p := l.prev; p.Typ {
	case itemIdent:
		return !exprKeywords[p.Val]
	case itemBracketString:
		return true
	case itemPunct:
		return p.Val == ")" || p.Val == "]"
	}
	return false
}

// jsxTagAhead reports whether the '<' at l.pos starts a JSX tag rather than
// a TypeScript generic parameter list such as <T,> or <T extends U>.
func (l *Lexer) jsxTagAhead() bool {
	i := l.pos + 1
	r, w := l.runeAt(i)
	if r == '>' {
		return true // fragment
	}
	if !isIdentStart(r) {
		return false
	}
	for isTagNamePart(r) {
		i += w
		r, w = l.runeAt(i)
	}
	rest := l.input[skipSpaceAt(l.input, i):]
	if strings.HasPrefix(rest, ",") {
		return false
	}
	if strings.HasPrefix(rest, "extends") && len(rest) > 7 && isSpace(rune(rest[7])) {
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Frames

func (l *Lexer) top() *frame { return &l.frames[len(l.frames)-1] }

func (l *Lexer) push(k frameKind) {
	l.frames = append(l.frames, frame{kind: k, pos: l.pos})
}

func (l *Lexer) pop() {
	if len(l.frames) > 1 {
		l.frames = l.frames[:len(l.frames)-1]
	}
}

// closeChildren leaves the children context of element name. Like the
// parser, it treats elements opened inside it as unclosed, and ignores a
// closing tag that matches no open element.
func (l *Lexer) closeChildren(name string) {
	for i := len(l.frames) - 1; i > 0 && l.frames[i].kind == frameChildren; i-- {
		if l.frames[i].tag == name {
			l.frames = l.frames[:i]
			return
		}
	}
}

// resume returns the state for the context on top of the frame stack.
func (l *Lexer) resume() StateFunc {
	switch l.top().kind {
	case frameTemplate:
		return lexTemplate
	case frameTag:
		return lexAttributes
	case frameChildren:
		return lexChildren
	default:
		return lexCode
	}
}

// ---------------------------------------------------------------------------
// Emitting

func (l *Lexer) send(it Item) {
	if l.stopped {
		return
	}
	select {
	case <-l.done:
		l.stopped = true
		return
	default:
	}
	select {
	case l.tokens <- it:
	case <-l.done:
		l.stopped = true
	}
}

func (l *Lexer) emit(t ItemType) {
	line, col := l.position(l.start)
	it := Item{Typ: t, Val: l.input[l.start:l.pos], Pos: l.start, Line: line, Col: col}
	l.send(it)
	l.start = l.pos
	l.prev, l.hasPrev = it, true
}

func (l *Lexer) emitNonEmpty(t ItemType) {
	if l.pos > l.start {
		l.emit(t)
	}
}

// emitText emits pending JSX text, dropping it if it is only whitespace.
func (l *Lexer) emitText() {
	if strings.TrimSpace(l.input[l.start:l.pos]) != "" {
		l.emit(itemText)
	} else {
		l.ignore()
	}
}

func (l *Lexer) errorf(pos int, format string, args ...any) {
	line, col := l.position(pos)
	l.send(Item{Typ: itemError, Val: fmt.Sprintf(format, args...), Pos: pos, Line: line, Col: col})
}

func (l *Lexer) position(off int) (line, col int) {
	i := sort.Search(len(l.lineStarts), func(i int) bool { return l.lineStarts[i] > off }) - 1
	return i + 1, off - l.lineStarts[i] + 1
}

// ---------------------------------------------------------------------------
// Input helpers

func (l *Lexer) next() rune {
	if l.pos >= len(l.input) {
		l.width = 0
		return eof
	}
	r, w := utf8.DecodeRuneInString(l.input[l.pos:])
	l.width = w
	l.pos += w
	return r
}

func (l *Lexer) backup() { l.pos -= l.width }

func (l *Lexer) peek() rune {
	r := l.next()
	l.backup()
	return r
}

// byteAt returns the byte at l.pos+off as a rune, or eof.
func (l *Lexer) byteAt(off int) rune {
	if i := l.pos + off; i < len(l.input) {
		return rune(l.input[i])
	}
	return eof
}

func (l *Lexer) runeAt(i int) (rune, int) {
	if i >= len(l.input) {
		return eof, 0
	}
	return utf8.DecodeRuneInString(l.input[i:])
}

func (l *Lexer) rest() string { return l.input[l.pos:] }

func (l *Lexer) ignore() { l.start = l.pos }

func (l *Lexer) acceptRun(valid func(rune) bool) {
	for {
		r := l.next()
		if r == eof {
			return
		}
		if !valid(r) {
			l.backup()
			return
		}
	}
}

func (l *Lexer) skipSpace() {
	l.acceptRun(isSpace)
	l.ignore()
}

func skipSpaceAt(s string, i int) int {
	for i < len(s) && isSpace(rune(s[i])) {
		i++
	}
	return i
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v'
}

func isDigit(r rune) bool { return '0' <= r && r <= '9' }

func isIdentStart(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r)
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || unicode.IsDigit(r)
}

func isNumberPart(r rune) bool {
	return isIdentPart(r) || r == '.'
}

func isTagNamePart(r rune) bool {
	return isIdentPart(r) || r == '-' || r == '.' || r == ':'
}

func isAttrNameStart(r rune) bool { return isIdentStart(r) }

func isAttrNamePart(r rune) bool {
	return isIdentPart(r) || r == '-' || r == ':'
}

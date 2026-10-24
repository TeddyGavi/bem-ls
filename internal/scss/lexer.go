// Package scss parses SCSS and CSS into rules, class definitions, symbols
// (variables, mixins, functions, placeholders, custom properties,
// keyframes) and references to them.
package scss

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/TeddyGavi/bem-ls/internal/core"
)

// ItemType identifies a token.
type ItemType int

// Item is a token. Except for itemError, Val is input[Pos:Pos+len(Val)] of
// the text being lexed; Pos, Line and Col are shifted by the lexer's base
// position (see LexAt).
type Item struct {
	Typ  ItemType
	Val  string
	Pos  int
	Line int
	Col  int
}

const (
	itemError       ItemType = iota // Val is the message
	itemEOF                         //
	itemSpace                       // run of whitespace
	itemComment                     // /* ... */ or // ... including delimiters
	itemIdent                       // color, -webkit-box, --custom
	itemVariable                    // $name
	itemPlaceholder                 // %name
	itemAtKeyword                   // @media
	itemHash                        // #fff, #id
	itemNumber                      // 10, 1.5em, 50%
	itemString                      // contents of '...' or "...", without quotes
	itemURL                         // contents of an unquoted url(...)
	itemInterpOpen                  // #{
	itemPunct                       // any other single character
)

var itemNames = map[ItemType]string{
	itemError: "itemError", itemEOF: "itemEOF", itemSpace: "itemSpace", itemComment: "itemComment",
	itemIdent: "itemIdent", itemVariable: "itemVariable", itemPlaceholder: "itemPlaceholder",
	itemAtKeyword: "itemAtKeyword", itemHash: "itemHash", itemNumber: "itemNumber",
	itemString: "itemString", itemURL: "itemURL", itemInterpOpen: "itemInterpOpen", itemPunct: "itemPunct",
}

func (t ItemType) String() string {
	if n, ok := itemNames[t]; ok {
		return n
	}
	return fmt.Sprintf("ItemType(%d)", int(t))
}

func (i Item) String() string { return fmt.Sprintf("%d:%d %s %q", i.Line, i.Col, i.Typ, i.Val) }

const eof = -1

const tokenBuffer = 64

// Lexer tokenizes SCSS/CSS in its own goroutine.
type Lexer struct {
	input      string
	start, pos int
	width      int
	base       core.Position
	tokens     chan Item
	done       chan struct{}
	stopOnce   sync.Once
	stopped    bool
	lineStarts []int
}

// StateFunc is a lexer state.
type StateFunc func(*Lexer) StateFunc

// Lex starts lexing input. The channel is closed after itemEOF. Call Stop
// if you stop reading early.
func Lex(input string) (*Lexer, <-chan Item) {
	return LexAt(input, core.Position{Line: 1, Col: 1})
}

// LexAt is like Lex for text that starts at base in a larger document, such
// as the CSS of a styled-components template. Positions are reported in the
// larger document.
func LexAt(input string, base core.Position) (*Lexer, <-chan Item) {
	if base.Line == 0 {
		base.Line = 1
	}
	if base.Col == 0 {
		base.Col = 1
	}
	l := &Lexer{input: input, base: base, tokens: make(chan Item, tokenBuffer), done: make(chan struct{})}
	go l.run()
	return l, l.tokens
}

// Stop makes the lexer goroutine finish early.
func (l *Lexer) Stop() { l.stopOnce.Do(func() { close(l.done) }) }

func (l *Lexer) run() {
	defer close(l.tokens)
	l.lineStarts = append(l.lineStarts, 0)
	for i := 0; i < len(l.input); i++ {
		if l.input[i] == '\n' {
			l.lineStarts = append(l.lineStarts, i+1)
		}
	}
	for state := lexMain; state != nil && !l.stopped; {
		state = state(l)
	}
}

// lexMain is the only state: CSS has no lexical modes beyond strings,
// comments and url(), which sub-lexers handle.
func lexMain(l *Lexer) StateFunc {
	for !l.stopped {
		r := l.peek()
		switch {
		case r == eof:
			l.emit(itemEOF)
			return nil
		case isSpace(r):
			l.acceptRun(isSpace)
			l.emit(itemSpace)
		case r == '/' && l.byteAt(1) == '/':
			if i := strings.IndexByte(l.rest(), '\n'); i >= 0 {
				l.pos += i
			} else {
				l.pos = len(l.input)
			}
			l.emit(itemComment)
		case r == '/' && l.byteAt(1) == '*':
			if i := strings.Index(l.input[l.pos+2:], "*/"); i >= 0 {
				l.pos += 2 + i + 2
				l.emit(itemComment)
			} else {
				open := l.pos
				l.pos = len(l.input)
				l.emit(itemComment)
				l.errorf(open, "unterminated comment")
			}
		case r == '"' || r == '\'':
			l.lexString()
		case r == '#' && l.byteAt(1) == '{':
			l.pos += 2
			l.emit(itemInterpOpen)
		case r == '#' && isNameByte(l.byteAt(1)):
			l.next()
			l.acceptRun(isNameRune)
			l.emit(itemHash)
		case r == '$' && l.identStartsAt(1):
			l.next()
			l.acceptRun(isNameRune)
			l.emit(itemVariable)
		case r == '%' && l.identStartsAt(1):
			l.next()
			l.acceptRun(isNameRune)
			l.emit(itemPlaceholder)
		case r == '@' && l.identStartsAt(1):
			l.next()
			l.acceptRun(isNameRune)
			l.emit(itemAtKeyword)
		case isDigit(r) || (r == '.' && isDigit(l.byteAt(1))):
			l.lexNumber()
		case l.identStartsAt(0):
			l.lexIdent()
		default:
			l.next()
			l.emit(itemPunct)
		}
	}
	return nil
}

func (l *Lexer) lexString() {
	quote := l.next()
	open := l.pos - 1
	l.start = l.pos
	for {
		switch r := l.next(); r {
		case '\\':
			l.next()
		case quote:
			l.backup()
			l.emit(itemString)
			l.next()
			l.start = l.pos
			return
		case '\n', eof:
			l.backup()
			l.emit(itemString)
			l.errorf(open, "unterminated string")
			return
		}
	}
}

func (l *Lexer) lexNumber() {
	l.acceptRun(isDigit)
	if l.peek() == '.' && isDigit(l.byteAt(1)) {
		l.next()
		l.acceptRun(isDigit)
	}
	if (l.peek() == 'e' || l.peek() == 'E') && (isDigit(l.byteAt(1)) ||
		((l.byteAt(1) == '-' || l.byteAt(1) == '+') && isDigit(l.byteAt(2)))) {
		l.pos += 2
		l.acceptRun(isDigit)
	}
	if l.peek() == '%' {
		l.next()
	} else if l.identStartsAt(0) {
		l.acceptRun(isNameRune) // unit
	}
	l.emit(itemNumber)
}

func (l *Lexer) lexIdent() {
	l.acceptName()
	name := l.input[l.start:l.pos]
	if strings.EqualFold(name, "url") && l.peek() == '(' {
		// Unquoted url(...) may contain "//", which is not a comment.
		j := skipSpaceAt(l.input, l.pos+1)
		if j < len(l.input) && l.input[j] != '"' && l.input[j] != '\'' {
			l.emit(itemIdent)
			l.next()
			l.emit(itemPunct)
			end := strings.IndexByte(l.rest(), ')')
			if end < 0 {
				open := l.pos
				l.pos = len(l.input)
				l.emit(itemURL)
				l.errorf(open, "unterminated url(")
				return
			}
			l.pos += end
			l.emit(itemURL)
			return
		}
	}
	l.emit(itemIdent)
}

// acceptName consumes name characters, including escapes like \:.
func (l *Lexer) acceptName() {
	for {
		r := l.next()
		switch {
		case r == '\\':
			if l.next() == eof {
				return
			}
		case r != eof && isNameRune(r):
		default:
			l.backup()
			return
		}
	}
}

// identStartsAt reports whether an identifier starts at l.pos+off: a name
// start, an escape, or '-' followed by one of those or another '-'.
func (l *Lexer) identStartsAt(off int) bool {
	r, w := l.runeAt(l.pos + off)
	switch {
	case r == '\\':
		return true
	case isNameStart(r):
		return true
	case r == '-':
		r2, _ := l.runeAt(l.pos + off + w)
		return r2 == '-' || r2 == '\\' || isNameStart(r2)
	}
	return false
}

// ---------------------------------------------------------------------------

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
	p := l.position(l.start)
	l.send(Item{Typ: t, Val: l.input[l.start:l.pos], Pos: p.Offset, Line: p.Line, Col: p.Col})
	l.start = l.pos
}

func (l *Lexer) errorf(off int, format string, args ...any) {
	p := l.position(off)
	l.send(Item{Typ: itemError, Val: fmt.Sprintf(format, args...), Pos: p.Offset, Line: p.Line, Col: p.Col})
}

// position converts an input offset into a position in the document.
func (l *Lexer) position(off int) core.Position {
	i := sort.Search(len(l.lineStarts), func(i int) bool { return l.lineStarts[i] > off }) - 1
	col := off - l.lineStarts[i] + 1
	if i == 0 {
		col += l.base.Col - 1
	}
	return core.Position{Offset: l.base.Offset + off, Line: l.base.Line + i, Col: col}
}

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

func skipSpaceAt(s string, i int) int {
	for i < len(s) && isSpace(rune(s[i])) {
		i++
	}
	return i
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f'
}

func isDigit(r rune) bool { return '0' <= r && r <= '9' }

func isNameStart(r rune) bool {
	return r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || (r >= utf8.RuneSelf && unicode.IsPrint(r))
}

func isNameRune(r rune) bool { return isNameStart(r) || isDigit(r) || r == '-' }

func isNameByte(r rune) bool { return r != eof && (isNameRune(r) || r >= utf8.RuneSelf) }

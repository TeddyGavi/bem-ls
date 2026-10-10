package core

// CompletionKind says what can be completed at a position.
type CompletionKind int

const (
	CompleteNone CompletionKind = iota
	// CompleteClass: inside a class string, e.g. className="card__|" or a
	// string in className={clsx('|')}.
	CompleteClass
	// CompleteModuleMember: after a CSS-module identifier, styles.| or
	// styles['|'].
	CompleteModuleMember
)

// CompletionContext describes the word being typed at an offset.
type CompletionContext struct {
	Kind CompletionKind
	// Prefix is the part of the word before the cursor.
	Prefix string
	// Start and End are the offsets of the whole word under the cursor,
	// which a completion replaces.
	Start, End int
	// Module is the identifier for CompleteModuleMember.
	Module string
	// Bracket is true for styles['...']; Start/End then cover the quoted
	// text only.
	Bracket bool
	// Dot is the offset of the '.' in styles.x (CompleteModuleMember
	// without Bracket), so a completion can switch to styles['a-b'].
	Dot int
	// Siblings are the other words in the same class string.
	Siblings []string
}

// CompletionContextAt finds what can be completed at offset in src.
// isModule reports whether an identifier is a CSS module; classAttrs are
// the class attributes (nil for the defaults).
func CompletionContextAt(src string, offset int, classAttrs []string, isModule func(string) bool) CompletionContext {
	if len(classAttrs) == 0 {
		classAttrs = []string{"className", "class"}
	}
	l, ch := Lex("complete", src)
	defer func() {
		l.Stop()
		for range ch {
		}
	}()
	var toks []Item
	for it := range ch {
		if it.Typ == itemError {
			continue
		}
		toks = append(toks, it)
		if it.Pos > offset+1 && len(toks) > 4 {
			// Read a few tokens past the cursor, then stop the lexer.
			if toks[len(toks)-4].Pos > offset {
				break
			}
		}
	}

	// Track whether we are in a class attribute's value.
	inClass := false  // in the expression of a class attribute
	depth := 0        // brace depth inside that expression
	pendingAttr := "" // attribute name waiting for its value
	for i, t := range toks {
		end := t.Pos + len(t.Val)
		switch t.Typ {
		case itemAttrName:
			pendingAttr = t.Val
		case itemEqual:
		case itemRawString:
			if contains(classAttrs, pendingAttr) && !inClass && t.Pos <= offset && offset <= end {
				return wordContext(CompleteClass, t, offset)
			}
			pendingAttr = ""
		case itemOpenBrace:
			if inClass {
				depth++
			} else if contains(classAttrs, pendingAttr) && i > 0 && toks[i-1].Typ == itemEqual {
				inClass, depth = true, 0
			}
			pendingAttr = ""
		case itemCloseBrace:
			if inClass {
				if depth == 0 {
					inClass = false
				} else {
					depth--
				}
			}
		case itemString, itemTemplatePart:
			if inClass && t.Pos <= offset && offset <= end {
				return wordContext(CompleteClass, t, offset)
			}
		case itemDot:
			// styles.| or styles.pre|
			if i == 0 || toks[i-1].Typ != itemIdent || isModule == nil || !isModule(toks[i-1].Val) {
				continue
			}
			if offset == end {
				return CompletionContext{Kind: CompleteModuleMember, Module: toks[i-1].Val, Start: end, End: end, Dot: t.Pos}
			}
			if i+1 < len(toks) && toks[i+1].Typ == itemIdent && toks[i+1].Pos == end {
				n := toks[i+1]
				if n.Pos <= offset && offset <= n.Pos+len(n.Val) {
					return CompletionContext{
						Kind: CompleteModuleMember, Module: toks[i-1].Val, Prefix: n.Val[:offset-n.Pos],
						Start: n.Pos, End: n.Pos + len(n.Val), Dot: t.Pos,
					}
				}
			}
		case itemBracketString:
			if i == 0 || toks[i-1].Typ != itemIdent || isModule == nil || !isModule(toks[i-1].Val) {
				continue
			}
			if t.Pos <= offset && offset <= end {
				return CompletionContext{
					Kind: CompleteModuleMember, Module: toks[i-1].Val, Bracket: true,
					Prefix: t.Val[:offset-t.Pos], Start: t.Pos, End: end,
				}
			}
		}
		if t.Pos > offset {
			break
		}
	}
	return CompletionContext{}
}

// wordContext returns the whitespace-separated word of t at offset.
func wordContext(kind CompletionKind, t Item, offset int) CompletionContext {
	k := offset - t.Pos
	s := t.Val
	start := k
	for start > 0 && !isSpace(rune(s[start-1])) {
		start--
	}
	end := k
	for end < len(s) && !isSpace(rune(s[end])) {
		end++
	}
	ctx := CompletionContext{Kind: kind, Prefix: s[start:k], Start: t.Pos + start, End: t.Pos + end}
	for _, w := range wordsOf(t, 0, len(s), ClassStatic, false, false) {
		if w.Range.Start.Offset != ctx.Start {
			ctx.Siblings = append(ctx.Siblings, w.Name)
		}
	}
	return ctx
}

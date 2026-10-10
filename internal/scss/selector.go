package scss

import "strings"

// span is a piece of selector text at an offset in the source.
type span struct {
	text string
	off  int // offset of text[0] in the document
}

// splitSelectorList splits a selector list at top-level commas, trimming
// each part.
func splitSelectorList(s span) []span {
	var out []span
	depth, start := 0, 0
	add := func(end int) {
		part := s.text[start:end]
		lead := len(part) - len(strings.TrimLeft(part, " \t\r\n\f"))
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, span{text: part, off: s.off + start + lead})
		}
	}
	for i := 0; i < len(s.text); i++ {
		switch c := s.text[i]; c {
		case '(', '[':
			depth++
		case ')', ']':
			if depth > 0 {
				depth--
			}
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		case '"', '\'':
			i = skipQuoted(s.text, i)
		case '\\':
			i++
		case ',':
			if depth == 0 {
				add(i)
				start = i + 1
			}
		}
	}
	add(len(s.text))
	return out
}

// resolveSelectors combines a rule's own selector list with its parents'
// selectors, replacing & or prefixing with a descendant combinator. With no
// parents the selectors are returned as written.
func resolveSelectors(parents []string, own []span) []string {
	var out []string
	for _, o := range own {
		if len(parents) == 0 {
			out = append(out, collapseSpace(o.text))
			continue
		}
		for _, p := range parents {
			if hasAmpersand(o.text) {
				out = append(out, collapseSpace(strings.ReplaceAll(o.text, "&", p)))
			} else {
				out = append(out, collapseSpace(p+" "+o.text))
			}
		}
	}
	return out
}

func hasAmpersand(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '&':
			return true
		case '"', '\'':
			i = skipQuoted(s, i)
		case '\\':
			i++
		}
	}
	return false
}

// classHit is a class name found in a selector, with the source offsets of
// the text that produced it.
type classHit struct {
	name       string
	start, end int
}

// placeholderHit is a %placeholder in a selector.
type placeholderHit = classHit

// scanSelector finds the classes and placeholders that a rule's own selector
// text defines. A class is ".name", or "&suffix" when a parent selector ends
// in a class. Names that touch an interpolation (.btn-#{$x}) are skipped
// because they are not known.
func scanSelector(s span, parents []string) (classes []classHit, placeholders []placeholderHit) {
	t := s.text
	for i := 0; i < len(t); i++ {
		switch c := t[i]; {
		case c == '"' || c == '\'':
			i = skipQuoted(t, i)
		case c == '[':
			i = skipTo(t, i, ']')
		case c == '#' && i+1 < len(t) && t[i+1] == '{':
			i = skipTo(t, i+1, '}')
		case c == '\\':
			i++
		case c == '.' || c == '%':
			j := scanName(t, i+1)
			if j == i+1 || touchesInterp(t, j) {
				i = j - 1
				continue
			}
			hit := classHit{name: t[i+1 : j], start: s.off + i, end: s.off + j}
			if c == '.' {
				classes = append(classes, hit)
			} else {
				placeholders = append(placeholders, hit)
			}
			i = j - 1
		case c == '&':
			j := scanName(t, i+1)
			if j == i+1 || touchesInterp(t, j) {
				i = j - 1
				continue
			}
			suffix := t[i+1 : j]
			seen := map[string]bool{}
			for _, p := range parents {
				if base, ok := trailingClass(p); ok && !seen[base+suffix] {
					seen[base+suffix] = true
					classes = append(classes, classHit{name: base + suffix, start: s.off + i, end: s.off + j})
				}
			}
			i = j - 1
		}
	}
	return classes, placeholders
}

// trailingClass returns "card" if selector ends with the class ".card".
func trailingClass(sel string) (string, bool) {
	j := len(sel)
	i := j
	for i > 0 && isNameByteASCII(sel[i-1]) {
		i--
	}
	if i == j || i == 0 || sel[i-1] != '.' {
		return "", false
	}
	if i >= 2 && sel[i-2] == '\\' {
		return "", false
	}
	return sel[i:j], true
}

// scanName returns the end of a CSS name starting at i (escapes allowed).
func scanName(t string, i int) int {
	for i < len(t) {
		switch c := t[i]; {
		case c == '\\' && i+1 < len(t):
			i += 2
		case isNameByteASCII(c) || c >= 0x80:
			i++
		default:
			return i
		}
	}
	return i
}

func touchesInterp(t string, i int) bool {
	return i+1 < len(t) && t[i] == '#' && t[i+1] == '{'
}

func isNameByteASCII(c byte) bool {
	return c == '-' || c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
}

// skipQuoted returns the index of the closing quote of the string that
// starts at i. An unterminated string ends before the newline, as in CSS.
func skipQuoted(t string, i int) int {
	q := t[i]
	for j := i + 1; j < len(t); j++ {
		switch t[j] {
		case '\\':
			j++
		case q:
			return j
		case '\n':
			return j - 1
		}
	}
	return len(t) - 1
}

// skipTo returns the index of the first close at or after i+1, or the last
// index.
func skipTo(t string, i int, close byte) int {
	if j := strings.IndexByte(t[i+1:], close); j >= 0 {
		return i + 1 + j
	}
	return len(t) - 1
}

func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

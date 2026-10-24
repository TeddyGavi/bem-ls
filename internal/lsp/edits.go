package lsp

import (
	"strings"
	"unicode/utf8"

	"github.com/TeddyGavi/bem-ls/internal/bem"
	"github.com/TeddyGavi/bem-ls/internal/lsp/protocol"
	"github.com/TeddyGavi/bem-ls/internal/scss"
)

// addSelectors returns text with an empty rule added for each class it
// doesn't define yet. Classes are nested BEM-style under their parent rule
// (&__elem inside .block, &--mod inside the base), creating parents as
// needed; anything else is appended at the end.
func addSelectors(text string, classes []string, naming *bem.Naming) string {
	for _, c := range classes {
		text = addSelector(text, c, naming, 0)
	}
	return text
}

func addSelector(text, class string, naming *bem.Naming, depth int) string {
	sheet := scss.Parse("", text)
	if sheet.HasClass(class) || depth > 8 {
		return text
	}
	nm := naming.Parse(class)
	parent := ""
	if !nm.StructErr {
		switch {
		case nm.HasMod:
			parent = nm.Base()
		case nm.IsElement():
			parent = nm.Block
			for i := len(nm.Elements) - 1; i >= 1; i-- {
				if p := naming.Format(nm.Block, nm.Elements[:i], "", ""); sheet.HasClass(p) {
					parent = p
					break
				}
			}
		}
	}
	if parent == "" || parent == class || !strings.HasPrefix(class, parent) {
		return appendRule(text, "."+class)
	}
	rule := ruleFor(sheet, parent)
	if rule == nil {
		text = addSelector(text, parent, naming, depth+1)
		sheet = scss.Parse("", text)
		if rule = ruleFor(sheet, parent); rule == nil {
			return appendRule(text, "."+class)
		}
	}
	return insertChild(text, rule, "&"+class[len(parent):])
}

// ruleFor returns the rule whose only selector is exactly .class.
func ruleFor(sheet *scss.Sheet, class string) *scss.Node {
	for _, d := range sheet.ClassDefs(class) {
		if d.Rule != nil && len(d.Rule.Selectors) == 1 && d.Rule.Selectors[0] == "."+class {
			return d.Rule
		}
	}
	return nil
}

// insertChild adds "sel {}" as the last child of rule.
func insertChild(text string, rule *scss.Node, sel string) string {
	closeAt := rule.Close.Offset
	if closeAt > len(text) {
		closeAt = len(text)
	}
	lineStart := strings.LastIndexByte(text[:closeAt], '\n') + 1
	if strings.TrimSpace(text[lineStart:closeAt]) == "" && lineStart > rule.Open.Offset {
		indent := lineIndent(text, rule.Range.Start.Offset) + indentUnit(text)
		return text[:lineStart] + indent + sel + " {}\n" + text[lineStart:]
	}
	ruleIndent := lineIndent(text, rule.Range.Start.Offset)
	if open := rule.Open.Offset; open < closeAt && strings.TrimSpace(text[open+1:closeAt]) == "" {
		// Empty block "{}": expand it.
		return text[:open+1] + "\n" + ruleIndent + indentUnit(text) + sel + " {}\n" + ruleIndent + text[closeAt:]
	}
	// One-line block with content: ".a { x: y; }".
	ins := sel + " {} "
	if closeAt > 0 && text[closeAt-1] != ' ' {
		ins = " " + ins
	}
	return text[:closeAt] + ins + text[closeAt:]
}

// appendRule adds "sel {}" at the end, after a blank line.
func appendRule(text, sel string) string {
	switch {
	case strings.TrimSpace(text) == "":
		return sel + " {}\n"
	case strings.HasSuffix(text, "\n\n"):
	case strings.HasSuffix(text, "\n"):
		text += "\n"
	default:
		text += "\n\n"
	}
	return text + sel + " {}\n"
}

// diffEdit returns a single edit turning old into new: the span between
// their common prefix and suffix.
func diffEdit(m *mapper, old, new string) protocol.TextEdit {
	p := 0
	for p < len(old) && p < len(new) && old[p] == new[p] {
		p++
	}
	for p > 0 && p < len(old) && !utf8.RuneStart(old[p]) {
		p--
	}
	s := 0
	for s < len(old)-p && s < len(new)-p && old[len(old)-1-s] == new[len(new)-1-s] {
		s++
	}
	for s > 0 && !utf8.RuneStart(old[len(old)-s]) {
		s--
	}
	return protocol.TextEdit{
		Range:   m.rangeOfOffsets(p, len(old)-s),
		NewText: new[p : len(new)-s],
	}
}

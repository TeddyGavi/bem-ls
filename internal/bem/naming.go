// Package bem models BEM naming conventions and checks JSX class usage
// against them.
package bem

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Preset names accepted by NamingConfig.Preset.
const (
	PresetClassic = "classic" // block__elem--mod
	PresetKV      = "kv"      // block__elem--key_value
	PresetReact   = "react"   // Block-elem_mod_value
	PresetCustom  = "custom"  // separators and patterns from the config
)

// NamingConfig describes a naming convention. For the built-in presets only
// Preset is needed; any other field that is set overrides the preset. For
// "custom", the separators default to the classic ones and the patterns
// default to "any non-empty name".
type NamingConfig struct {
	Preset string `json:"preset,omitempty"`

	ElementSep  string `json:"elementSeparator,omitempty"`
	ModifierSep string `json:"modifierSeparator,omitempty"`
	ValueSep    string `json:"valueSeparator,omitempty"` // "" disables modifier values

	// Patterns for each part, without anchors (they are added).
	Block    string `json:"block,omitempty"`
	Element  string `json:"element,omitempty"`
	Modifier string `json:"modifier,omitempty"`
	Value    string `json:"value,omitempty"`
}

const (
	kebab    = `[a-z][a-z0-9]*(?:-[a-z0-9]+)*`
	kebabVal = `[a-z0-9]+(?:-[a-z0-9]+)*`
	pascal   = `[A-Z][a-zA-Z0-9]*`
	camel    = `[a-z][a-zA-Z0-9]*`
	camelish = `[a-zA-Z][a-zA-Z0-9]*`
	alnum    = `[a-zA-Z0-9]+`
	anyPart  = `.+`
)

var presets = map[string]NamingConfig{
	PresetClassic: {ElementSep: "__", ModifierSep: "--", Block: kebab, Element: kebab, Modifier: kebab},
	PresetKV: {ElementSep: "__", ModifierSep: "--", ValueSep: "_",
		Block: kebab, Element: kebab, Modifier: kebab, Value: kebabVal},
	PresetReact: {ElementSep: "-", ModifierSep: "_", ValueSep: "_",
		Block: pascal, Element: camelish, Modifier: camel, Value: alnum},
	PresetCustom: {ElementSep: "__", ModifierSep: "--",
		Block: anyPart, Element: anyPart, Modifier: anyPart, Value: anyPart},
}

// Naming is a compiled naming convention.
type Naming struct {
	preset                  string
	elemSep, modSep, valSep string
	block, elem, mod, val   *regexp.Regexp
	seps                    []sep // longest first
}

type sepKind uint8

const (
	sepNone sepKind = iota
	sepElem
	sepMod
	sepVal
)

type sep struct {
	s    string
	kind sepKind
}

// Classic returns the classic block__elem--mod convention.
func Classic() *Naming {
	n, err := NewNaming(NamingConfig{Preset: PresetClassic})
	if err != nil {
		panic(err)
	}
	return n
}

// NewNaming compiles a naming convention.
func NewNaming(cfg NamingConfig) (*Naming, error) {
	name := cfg.Preset
	if name == "" {
		name = PresetClassic
	}
	base, ok := presets[name]
	if !ok {
		return nil, fmt.Errorf("unknown naming preset %q (want classic, kv, react or custom)", name)
	}
	override := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	override(&base.ElementSep, cfg.ElementSep)
	override(&base.ModifierSep, cfg.ModifierSep)
	override(&base.ValueSep, cfg.ValueSep)
	override(&base.Block, cfg.Block)
	override(&base.Element, cfg.Element)
	override(&base.Modifier, cfg.Modifier)
	override(&base.Value, cfg.Value)

	if base.ElementSep == "" || base.ModifierSep == "" {
		return nil, fmt.Errorf("naming %q: element and modifier separators must not be empty", name)
	}
	if base.ElementSep == base.ModifierSep {
		return nil, fmt.Errorf("naming %q: element and modifier separators are both %q", name, base.ElementSep)
	}
	if base.ValueSep == base.ElementSep {
		return nil, fmt.Errorf("naming %q: value and element separators are both %q", name, base.ValueSep)
	}

	n := &Naming{preset: name, elemSep: base.ElementSep, modSep: base.ModifierSep, valSep: base.ValueSep}
	for _, p := range []struct {
		dst  **regexp.Regexp
		pat  string
		what string
	}{
		{&n.block, base.Block, "block"},
		{&n.elem, base.Element, "element"},
		{&n.mod, base.Modifier, "modifier"},
		{&n.val, base.Value, "value"},
	} {
		if p.pat == "" {
			p.pat = anyPart
		}
		re, err := regexp.Compile(`^(?:` + p.pat + `)$`)
		if err != nil {
			return nil, fmt.Errorf("naming %q: bad %s pattern: %v", name, p.what, err)
		}
		*p.dst = re
	}

	n.seps = []sep{{n.elemSep, sepElem}, {n.modSep, sepMod}}
	if n.valSep != "" && n.valSep != n.modSep {
		n.seps = append(n.seps, sep{n.valSep, sepVal})
	}
	sort.SliceStable(n.seps, func(i, j int) bool { return len(n.seps[i].s) > len(n.seps[j].s) })
	return n, nil
}

// Preset returns the preset the convention is based on.
func (n *Naming) Preset() string { return n.preset }

// ElementSep returns the separator before an element, e.g. "__".
func (n *Naming) ElementSep() string { return n.elemSep }

// ModifierSep returns the separator before a modifier, e.g. "--".
func (n *Naming) ModifierSep() string { return n.modSep }

// ValueSep returns the separator before a modifier value, or "".
func (n *Naming) ValueSep() string { return n.valSep }

// Name is a class name split into BEM parts.
type Name struct {
	Raw      string
	Block    string
	Elements []string // more than one means a nested element (b__e1__e2)
	Mod      string
	Val      string
	HasMod   bool
	HasVal   bool

	// Err says why the name breaks the convention; nil if it doesn't. The
	// other fields are still filled in as well as possible.
	Err error

	// StructErr is true if Err is about the shape of the name (misplaced or
	// empty parts) rather than a part not matching its pattern.
	StructErr bool

	modAt int // index of the modifier separator in Raw, or -1
}

// Base returns the name without its modifier: "b__e" for "b__e--m".
func (n Name) Base() string {
	if n.modAt < 0 {
		return n.Raw
	}
	return n.Raw[:n.modAt]
}

// ModSuffix returns the modifier including its separator: "--m" for
// "b__e--m", or "".
func (n Name) ModSuffix() string {
	if n.modAt < 0 {
		return ""
	}
	return n.Raw[n.modAt:]
}

// IsElement reports whether the name has at least one element part.
func (n Name) IsElement() bool { return len(n.Elements) > 0 }

// Valid reports whether the name follows the convention.
func (n Name) Valid() bool { return n.Err == nil }

type segment struct {
	kind       sepKind // separator in front of the segment
	start, end int
}

// Parse splits class into parts and checks it against the convention.
func (n *Naming) Parse(class string) Name {
	out := Name{Raw: class, modAt: -1}

	// Split on separators, longest match first.
	segs := []segment{{kind: sepNone}}
	for i := 0; i < len(class); {
		matched := false
		for _, s := range n.seps {
			if strings.HasPrefix(class[i:], s.s) {
				segs[len(segs)-1].end = i
				if s.kind == sepMod && out.modAt < 0 {
					out.modAt = i
				}
				segs = append(segs, segment{kind: s.kind, start: i + len(s.s)})
				i += len(s.s)
				matched = true
				break
			}
		}
		if !matched {
			i++
		}
	}
	segs[len(segs)-1].end = len(class)
	text := func(s segment) string { return class[s.start:s.end] }

	fail := func(structural bool, format string, args ...any) Name {
		if out.Err == nil {
			out.Err = fmt.Errorf(format, args...)
			out.StructErr = structural
		}
		return out
	}

	// Fill in the parts: block, elements, modifier, value.
	out.Block = text(segs[0])
	i := 1
	for ; i < len(segs) && segs[i].kind == sepElem; i++ {
		out.Elements = append(out.Elements, text(segs[i]))
	}
	if i < len(segs) && segs[i].kind == sepMod {
		out.HasMod, out.Mod = true, text(segs[i])
		i++
		if i < len(segs) && n.valSep != "" &&
			(segs[i].kind == sepVal || (segs[i].kind == sepMod && n.valSep == n.modSep)) {
			out.HasVal, out.Val = true, text(segs[i])
			i++
		}
	}
	if i < len(segs) {
		switch segs[i].kind {
		case sepElem:
			fail(true, "element %q comes after the modifier", text(segs[i]))
		case sepMod:
			fail(true, "more than one modifier")
		case sepVal:
			if n.valSep == "" || !out.HasMod {
				fail(true, "unexpected %q", n.valSep)
			} else {
				fail(true, "more than one modifier value")
			}
		}
	}

	// Check each part.
	check := func(part, what string, re *regexp.Regexp) {
		if part == "" {
			fail(true, "empty %s name", what)
		} else if !re.MatchString(part) {
			fail(false, "%s %q does not match %s", what, part, describe(re))
		}
	}
	check(out.Block, "block", n.block)
	for _, e := range out.Elements {
		check(e, "element", n.elem)
	}
	if out.HasMod {
		check(out.Mod, "modifier", n.mod)
	}
	if out.HasVal {
		check(out.Val, "modifier value", n.val)
	}
	return out
}

// describe turns a compiled part pattern into a short description for
// messages.
func describe(re *regexp.Regexp) string {
	switch strings.TrimSuffix(strings.TrimPrefix(re.String(), "^(?:"), ")$") {
	case kebab, kebabVal:
		return "lower-case kebab-case (e.g. my-name)"
	case pascal:
		return "PascalCase (e.g. MyName)"
	case camel:
		return "camelCase (e.g. myName)"
	case camelish:
		return "camelCase or PascalCase"
	case alnum:
		return "letters and digits"
	}
	return re.String()
}

// Format joins parts into a class name.
func (n *Naming) Format(block string, elements []string, mod, val string) string {
	var b strings.Builder
	b.WriteString(block)
	for _, e := range elements {
		b.WriteString(n.elemSep)
		b.WriteString(e)
	}
	if mod != "" {
		b.WriteString(n.modSep)
		b.WriteString(mod)
		if val != "" && n.valSep != "" {
			b.WriteString(n.valSep)
			b.WriteString(val)
		}
	}
	return b.String()
}

// Suggest returns a corrected spelling of an invalid name, or "" if it can't
// find one. It only fixes the case and word separators of each part, e.g.
// "card__semiAnnulus" becomes "card__semi-annulus" in the classic preset.
func (n *Naming) Suggest(name Name) string {
	if name.Valid() || name.StructErr || n.preset == PresetCustom {
		return ""
	}
	var block, elem, mod, val func(string) string
	switch n.preset {
	case PresetReact:
		block, elem, mod, val = toPascal, toCamelish, toCamel, toAlnum
	default:
		block, elem, mod, val = toKebab, toKebab, toKebab, toKebab
	}
	elems := make([]string, len(name.Elements))
	for i, e := range name.Elements {
		elems[i] = elem(e)
	}
	m, v := "", ""
	if name.HasMod {
		m = mod(name.Mod)
	}
	if name.HasVal {
		v = val(name.Val)
	}
	s := n.Format(block(name.Block), elems, m, v)
	if s == name.Raw || !n.Parse(s).Valid() {
		return ""
	}
	return s
}

// words splits "semiAnnulus", "semi_annulus" or "Semi-Annulus" into
// lower-case words.
func words(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case r == '-' || r == '_' || unicode.IsSpace(r):
			flush()
		case unicode.IsUpper(r):
			lowerBefore := i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1]))
			acronymEnd := i > 0 && unicode.IsUpper(rs[i-1]) && i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if lowerBefore || acronymEnd {
				flush()
			}
			cur = append(cur, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			cur = append(cur, r)
		default:
			flush()
		}
	}
	flush()
	return out
}

func toKebab(s string) string { return strings.Join(words(s), "-") }

func capitalize(w string) string {
	if w == "" {
		return w
	}
	r := []rune(w)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func toPascal(s string) string {
	ws := words(s)
	for i := range ws {
		ws[i] = capitalize(ws[i])
	}
	return strings.Join(ws, "")
}

func toCamel(s string) string {
	ws := words(s)
	for i := 1; i < len(ws); i++ {
		ws[i] = capitalize(ws[i])
	}
	return strings.Join(ws, "")
}

// toCamelish keeps an initial capital: element names may be either case.
func toCamelish(s string) string {
	if s != "" && unicode.IsUpper([]rune(s)[0]) {
		return toPascal(s)
	}
	return toCamel(s)
}

func toAlnum(s string) string { return strings.Join(words(s), "") }

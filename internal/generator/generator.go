// Package generator turns a parsed JSX tree into an SCSS skeleton.
package generator

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"scss-bem-gen/internal/bem"
	"scss-bem-gen/internal/core"
)

// Nesting selects how rules are nested in the output.
type Nesting int

const (
	// NestingDOM follows the JSX tree. A class is written as &__x inside
	// the nearest enclosing rule whose class is a BEM prefix of it, and as a
	// nested descendant selector (.x) otherwise. Bare intrinsic elements
	// without classed descendants become tag selectors.
	NestingDOM Nesting = iota
	// NestingBEM ignores the JSX tree and nests by class name only: every
	// block is a top-level rule, and each element sits under the longest
	// class used in the markup that is a BEM prefix of it. Tag selectors are
	// not generated.
	NestingBEM
)

// ParseNesting parses "dom" or "bem".
func ParseNesting(s string) (Nesting, error) {
	switch strings.ToLower(s) {
	case "dom", "":
		return NestingDOM, nil
	case "bem":
		return NestingBEM, nil
	}
	return 0, fmt.Errorf("unknown nesting %q (want dom or bem)", s)
}

func (n Nesting) String() string {
	if n == NestingBEM {
		return "bem"
	}
	return "dom"
}

// Options control the output.
type Options struct {
	Indent  string // one level of indentation; default two spaces
	Nesting Nesting
	Naming  *bem.Naming // how class names split into parts; default classic
}

// Generate returns the SCSS skeleton for the tree rooted at root, which is
// normally the virtual root returned by core.Parser.Root. Only static and
// conditional classes are used: CSS-module and dynamic classes are skipped.
// It returns "" if there is nothing to generate.
func Generate(root *core.Node, opts Options) string {
	if opts.Indent == "" {
		opts.Indent = "  "
	}
	if opts.Naming == nil {
		opts.Naming = bem.Classic()
	}
	c := common{naming: opts.Naming, elemSep: opts.Naming.ElementSep(), out: newRule("", "")}
	switch opts.Nesting {
	case NestingBEM:
		g := &bemGen{common: c, byName: map[string]*rule{}, used: map[string]bool{}}
		g.collect(root)
		g.walk(root)
	default:
		g := &domGen{common: c}
		g.walk(root, nil)
	}
	out := c.out
	var b strings.Builder
	for i, r := range out.ordered() {
		if i > 0 {
			b.WriteByte('\n')
		}
		r.write(&b, opts.Indent, 0)
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Rule tree

type rule struct {
	sel      string // as written: ".card", "&__title", "&--active", "img"
	class    string // full class it matches, without modifier; "" for tags
	isMod    bool
	children []*rule
	index    map[string]*rule
}

func newRule(sel, class string) *rule {
	return &rule{sel: sel, class: class, index: map[string]*rule{}}
}

// child returns the child rule with selector sel, creating it if needed, so
// duplicate selectors are merged.
func (r *rule) child(sel, class string) *rule {
	if c, ok := r.index[sel]; ok {
		return c
	}
	c := newRule(sel, class)
	r.index[sel] = c
	r.children = append(r.children, c)
	return c
}

// addModifiers adds &--m rules; mods are suffixes such as "--m".
func (r *rule) addModifiers(base string, mods []string) {
	for _, m := range mods {
		r.child("&"+m, base+m).isMod = true
	}
}

// ordered returns modifiers first, then the other children, each in order of
// first appearance.
func (r *rule) ordered() []*rule {
	out := make([]*rule, 0, len(r.children))
	for _, c := range r.children {
		if c.isMod {
			out = append(out, c)
		}
	}
	for _, c := range r.children {
		if !c.isMod {
			out = append(out, c)
		}
	}
	return out
}

func (r *rule) write(b *strings.Builder, indent string, depth int) {
	pad := strings.Repeat(indent, depth)
	b.WriteString(pad)
	b.WriteString(r.sel)
	if len(r.children) == 0 {
		b.WriteString(" {}\n")
		return
	}
	b.WriteString(" {\n")
	for _, c := range r.ordered() {
		c.write(b, indent, depth+1)
	}
	b.WriteString(pad)
	b.WriteString("}\n")
}

// ---------------------------------------------------------------------------
// Class helpers

// common is shared by both nesting modes.
type common struct {
	naming  *bem.Naming
	elemSep string
	out     *rule
}

// base is a class without its modifier, plus the modifier suffixes used
// with it on one element.
type base struct {
	name string
	mods []string // e.g. "--big"
}

// basesOf groups an element's usable classes by base, in order. Both
// "card card--big" and a lone "card--big" give {card [--big]}.
func (g *common) basesOf(n *core.Node) []base {
	var out []base
	for _, c := range n.Classes {
		if c.Kind != core.ClassStatic && c.Kind != core.ClassConditional {
			continue
		}
		if !validIdent(c.Name) {
			continue
		}
		parsed := g.naming.Parse(c.Name)
		name, mod := parsed.Base(), parsed.ModSuffix()
		if name == "" {
			continue
		}
		i := 0
		for i < len(out) && out[i].name != name {
			i++
		}
		if i == len(out) {
			out = append(out, base{name: name})
		}
		if mod != "" && !contains(out[i].mods, mod) {
			out[i].mods = append(out[i].mods, mod)
		}
	}
	return out
}

// validIdent reports whether s can be written as a class selector without
// escaping. Utility classes like "w-1/2" or "hover:x" are skipped.
func validIdent(s string) bool {
	t := strings.TrimPrefix(s, "-")
	if t == "" {
		return false
	}
	for i, r := range t {
		ok := r == '_' || r >= utf8.RuneSelf || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z')
		if i > 0 {
			ok = ok || r == '-' || ('0' <= r && r <= '9')
		}
		if !ok {
			return false
		}
	}
	return true
}

func (g *common) hasClassedDescendant(n *core.Node) bool {
	for _, c := range n.Children {
		if len(g.basesOf(c)) > 0 || g.hasClassedDescendant(c) {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// DOM nesting

type domGen struct{ common }

// level holds the rules created for one classed ancestor element.
type level []*rule

func (g *domGen) container(chain []level) *rule {
	if len(chain) == 0 {
		return g.out
	}
	return chain[len(chain)-1][0]
}

func (g *domGen) walk(n *core.Node, chain []level) {
	bases := g.basesOf(n)
	switch {
	case len(bases) > 0:
		var lv level
		for _, b := range bases {
			r := g.place(b.name, chain)
			r.addModifiers(b.name, b.mods)
			lv = append(lv, r)
		}
		chain = append(chain, lv)
	case n.IsRoot() || n.IsFragment() || n.IsComponent() || len(n.Classes) > 0 || g.hasClassedDescendant(n):
		// Transparent: children are placed as if they were ours. Elements
		// with only module or dynamic classes are styled elsewhere.
	default:
		chain = append(chain, level{g.container(chain).child(n.Tag, "")})
	}
	for _, c := range n.Children {
		g.walk(c, chain)
	}
}

// place finds or creates the rule for class name. It looks for the nearest
// enclosing rule whose class equals name or is a BEM prefix of it. Failing
// that, the class is nested as a descendant selector, except at the top
// level, where an element goes under its block: .b { &__e } is the same
// selector as .b__e there.
func (g *domGen) place(name string, chain []level) *rule {
	for i := len(chain) - 1; i >= 0; i-- {
		for _, r := range chain[i] {
			if r.class == "" {
				continue
			}
			if r.class == name {
				return r
			}
			if strings.HasPrefix(name, r.class+g.elemSep) {
				return r.child("&"+name[len(r.class):], name)
			}
		}
	}
	c := g.container(chain)
	if block, _, ok := strings.Cut(name, g.elemSep); ok && block != "" && c == g.out {
		return g.out.child("."+block, block).child("&"+name[len(block):], name)
	}
	return c.child("."+name, name)
}

// ---------------------------------------------------------------------------
// BEM nesting

type bemGen struct {
	common
	byName map[string]*rule
	used   map[string]bool
}

func (g *bemGen) collect(n *core.Node) {
	for _, b := range g.basesOf(n) {
		g.used[b.name] = true
	}
	for _, c := range n.Children {
		g.collect(c)
	}
}

func (g *bemGen) walk(n *core.Node) {
	for _, b := range g.basesOf(n) {
		g.place(b.name).addModifiers(b.name, b.mods)
	}
	for _, c := range n.Children {
		g.walk(c)
	}
}

// place returns the rule for name, nesting it under the longest used class
// that is a BEM prefix of it, or under its block.
func (g *bemGen) place(name string) *rule {
	if r, ok := g.byName[name]; ok {
		return r
	}
	var r *rule
	block, _, isElem := strings.Cut(name, g.elemSep)
	if !isElem || block == "" {
		r = g.out.child("."+name, name)
	} else {
		parent := block
		for i := len(name) - len(g.elemSep); i > len(block); i-- {
			if strings.HasPrefix(name[i:], g.elemSep) && g.used[name[:i]] {
				parent = name[:i]
				break
			}
		}
		r = g.place(parent).child("&"+name[len(parent):], name)
	}
	g.byName[name] = r
	return r
}

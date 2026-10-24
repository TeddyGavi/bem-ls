package scss

import (
	"fmt"
	"strings"

	"github.com/TeddyGavi/bem-ls/internal/core"
)

// NodeKind is the kind of a Node.
type NodeKind int

const (
	KindRoot   NodeKind = iota // the virtual root
	KindRule                   // style rule: selector { ... }
	KindAtRule                 // @media, @mixin, @include with a block, ...
)

// Node is a rule with a block, or the virtual root.
type Node struct {
	Kind NodeKind
	// AtName is the at-rule name without "@" ("media", "mixin"); "" for
	// style rules.
	AtName string
	// Prelude is the raw selector or at-rule parameters, trimmed.
	Prelude      string
	PreludeRange core.Range
	// Selectors are the full selectors of a style rule with & resolved.
	// For at-rules they are the selectors their children nest in.
	Selectors []string
	Range     core.Range    // from the prelude to the end of the closing }
	Open      core.Position // the '{'
	Close     core.Position // the '}', or the end of input if it is missing
	Decls     []Decl
	Children  []*Node
	Parent    *Node
}

// Decl is a property declaration.
type Decl struct {
	Property  string
	Value     string
	PropRange core.Range
	Range     core.Range // through the ';' if present
}

// ClassDef is a class a style rule's own selector defines, e.g. "card__title"
// for &__title inside .card.
type ClassDef struct {
	Name  string
	Range core.Range // the source text that produced it: ".card" or "&__title"
	Rule  *Node
}

// SymbolKind is the kind of a Symbol or Ref.
type SymbolKind int

const (
	SymVariable       SymbolKind = iota // $name
	SymMixin                            // @mixin name
	SymFunction                         // @function name
	SymPlaceholder                      // %name
	SymCustomProperty                   // --name
	SymKeyframes                        // @keyframes name
)

func (k SymbolKind) String() string {
	switch k {
	case SymVariable:
		return "variable"
	case SymMixin:
		return "mixin"
	case SymFunction:
		return "function"
	case SymPlaceholder:
		return "placeholder"
	case SymCustomProperty:
		return "custom property"
	case SymKeyframes:
		return "keyframes"
	}
	return fmt.Sprintf("SymbolKind(%d)", int(k))
}

// Sigil returns the prefix written before a name of kind k: "$", "%" or "".
func (k SymbolKind) Sigil() string {
	switch k {
	case SymVariable:
		return "$"
	case SymPlaceholder:
		return "%"
	}
	return ""
}

// Symbol is a definition.
type Symbol struct {
	Kind SymbolKind
	// Name is written without its sigil: "primary" for $primary, "btn" for
	// %btn. Custom properties keep their dashes: "--gap".
	Name  string
	Range core.Range // the name as written, with sigil
	// Full covers the whole declaration or at-rule.
	Full core.Range
	// Scope is the rule the symbol is local to, or nil if it is global.
	Scope *Node
	// Value is a variable's or custom property's value; Params a mixin's or
	// function's parameter list, without parentheses.
	Value   string
	Params  string
	Default bool // !default
	Param   bool // a mixin/function parameter or loop variable
	// Doc is the text of the /// or /** */ comments right before it.
	Doc string
}

// Label returns the name with its sigil, e.g. "$primary".
func (s *Symbol) Label() string { return s.Kind.Sigil() + s.Name }

// Ref is a use of a symbol.
type Ref struct {
	Kind      SymbolKind
	Name      string // without sigil, like Symbol.Name
	Namespace string // "vars" in vars.$x; "" if none
	Range     core.Range
	// Scope is the innermost rule containing the reference; nil at the top
	// level.
	Scope *Node
}

// ImportKind is the kind of a stylesheet import.
type ImportKind int

const (
	ImportUse ImportKind = iota
	ImportForward
	ImportImport
)

func (k ImportKind) String() string {
	return [...]string{"@use", "@forward", "@import"}[k]
}

// Import is a @use, @forward or one URL of an @import.
type Import struct {
	Kind     ImportKind
	URL      string
	URLRange core.Range
	// Namespace for @use: the "as" name, the default derived from the URL,
	// or "*" for "as *".
	Namespace string
	// Prefix for "@forward ... as prefix-*".
	Prefix string
	// Show and Hide list the members named by @forward show/hide, with
	// sigils ("$x", "mixin-name").
	Show, Hide []string
	Range      core.Range
}

// Sheet is a parsed stylesheet.
type Sheet struct {
	Name    string
	Root    *Node
	Classes []ClassDef
	Symbols []*Symbol
	Refs    []*Ref
	Imports []Import
	Errors  core.ErrorList
}

// HasClass reports whether the sheet defines class. It implements
// bem.ClassSet.
func (s *Sheet) HasClass(name string) bool {
	for _, c := range s.Classes {
		if c.Name == name {
			return true
		}
	}
	return false
}

// ClassDefs returns the definitions of class.
func (s *Sheet) ClassDefs(name string) []ClassDef {
	var out []ClassDef
	for _, c := range s.Classes {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

// NodeAt returns the innermost node whose range contains offset.
func (s *Sheet) NodeAt(offset int) *Node {
	n := s.Root
	for {
		var next *Node
		for _, c := range n.Children {
			if c.Range.Start.Offset <= offset && offset <= c.Range.End.Offset {
				next = c
				break
			}
		}
		if next == nil {
			return n
		}
		n = next
	}
}

// SymbolAt returns the symbol whose name contains offset, if any.
func (s *Sheet) SymbolAt(offset int) *Symbol {
	for _, sym := range s.Symbols {
		if contains(sym.Range, offset) {
			return sym
		}
	}
	return nil
}

// RefAt returns the reference whose name contains offset, if any.
func (s *Sheet) RefAt(offset int) *Ref {
	for _, r := range s.Refs {
		if contains(r.Range, offset) {
			return r
		}
	}
	return nil
}

// ImportAt returns the import whose URL contains offset, if any.
func (s *Sheet) ImportAt(offset int) (Import, bool) {
	for _, imp := range s.Imports {
		if contains(imp.URLRange, offset) {
			return imp, true
		}
	}
	return Import{}, false
}

func contains(r core.Range, off int) bool { return r.Start.Offset <= off && off <= r.End.Offset }

// Lookup resolves ref against the definitions in this sheet only: local
// scopes first, then globals. Namespaced references are not resolved here.
func (s *Sheet) Lookup(ref *Ref) *Symbol {
	if ref.Namespace != "" {
		return nil
	}
	if ref.Kind == SymVariable {
		for scope := ref.Scope; scope != nil; scope = scope.Parent {
			if sym := s.lookupIn(ref.Kind, ref.Name, scope, ref.Range.Start.Offset); sym != nil {
				return sym
			}
		}
	}
	return s.lookupIn(ref.Kind, ref.Name, nil, ref.Range.Start.Offset)
}

// Global returns the global symbol of kind and name, preferring the first
// declaration.
func (s *Sheet) Global(kind SymbolKind, name string) *Symbol {
	return s.lookupIn(kind, name, nil, -1)
}

// lookupIn finds a symbol in scope; with before >= 0 it prefers the last
// declaration before that offset.
func (s *Sheet) lookupIn(kind SymbolKind, name string, scope *Node, before int) *Symbol {
	var first, best *Symbol
	for _, sym := range s.Symbols {
		if sym.Kind != kind || sym.Name != name || sym.Scope != scope {
			continue
		}
		if first == nil {
			first = sym
		}
		if before >= 0 && sym.Range.Start.Offset <= before {
			best = sym
		}
	}
	if best != nil {
		return best
	}
	return first
}

// DefaultNamespace returns the namespace @use gives a URL: its last path
// segment without leading "_" and extension. "sass:math" gives "math".
func DefaultNamespace(url string) string {
	if strings.HasPrefix(url, "sass:") {
		return strings.TrimPrefix(url, "sass:")
	}
	base := url
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimPrefix(base, "_")
	if i := strings.IndexByte(base, '.'); i > 0 {
		base = base[:i]
	}
	return base
}

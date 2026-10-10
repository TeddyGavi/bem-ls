package bem

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"scss-bem-gen/internal/core"
)

// Diagnostic is one finding.
type Diagnostic struct {
	Rule     Rule
	Severity Severity
	Range    core.Range
	Class    string
	Message  string
	// Suggestion is a replacement class name, if the linter has one.
	Suggestion string
}

func (d Diagnostic) String() string {
	s := fmt.Sprintf("%d:%d %s %s: %s", d.Range.Start.Line, d.Range.Start.Col, d.Severity, d.Rule, d.Message)
	if d.Suggestion != "" {
		s += fmt.Sprintf(" (did you mean %q?)", d.Suggestion)
	}
	return s
}

// ClassSet answers whether a stylesheet defines a class.
type ClassSet interface {
	HasClass(name string) bool
}

// NameSet is a ClassSet backed by a map.
type NameSet map[string]bool

// NewNameSet returns a NameSet holding names.
func NewNameSet(names ...string) NameSet {
	s := make(NameSet, len(names))
	for _, n := range names {
		s[n] = true
	}
	return s
}

// HasClass implements ClassSet.
func (s NameSet) HasClass(name string) bool { return s[name] }

// Linter checks class usage against a naming convention.
type Linter struct {
	naming *Naming
	sev    map[Rule]Severity
	ignore []*regexp.Regexp
}

// NewLinter builds a Linter from cfg.
func NewLinter(cfg Config) (*Linter, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	n, err := NewNaming(cfg.Naming)
	if err != nil {
		return nil, err
	}
	ign, err := compileIgnore(cfg.Ignore)
	if err != nil {
		return nil, err
	}
	sev := make(map[Rule]Severity, len(DefaultSeverities))
	for r, s := range DefaultSeverities {
		sev[r] = s
	}
	for r, s := range cfg.Rules {
		sev[r] = s
	}
	return &Linter{naming: n, sev: sev, ignore: ign}, nil
}

// Naming returns the linter's naming convention.
func (l *Linter) Naming() *Naming { return l.naming }

// Severity returns the configured severity of rule r.
func (l *Linter) Severity(r Rule) Severity { return l.sev[r] }

func (l *Linter) on(r Rule) bool { return l.sev[r] != SeverityOff }

func (l *Linter) ignored(class string) bool {
	for _, re := range l.ignore {
		if re.MatchString(class) {
			return true
		}
	}
	return false
}

// Input is what Lint checks.
type Input struct {
	// Root is the parsed JSX tree.
	Root *core.Node
	// Stylesheet holds the classes available to the component. If nil,
	// missingInStylesheet is skipped.
	Stylesheet ClassSet
	// Modules maps a CSS-module identifier (as in styles.x) to the classes
	// of the module it was imported from. Identifiers not in the map are
	// skipped by moduleClassMissing.
	Modules map[string]ClassSet
}

// usedClass is a ClassRef that BEM rules apply to, with its parsed name.
type usedClass struct {
	ref  core.ClassRef
	name Name
}

// Lint runs every enabled rule except unusedSelector (see LintStylesheet)
// and returns the findings ordered by position.
func (l *Linter) Lint(in Input) []Diagnostic {
	if in.Root == nil {
		return nil
	}
	c := &lintRun{l: l, in: in, parsed: map[*core.Node][]usedClass{}}
	if l.on(RuleNestedElement) {
		c.indexFlat(in.Root)
	}
	c.walk(in.Root)
	sortDiagnostics(c.diags)
	return c.diags
}

type lintRun struct {
	l      *Linter
	in     Input
	parsed map[*core.Node][]usedClass
	diags  []Diagnostic

	// For nestedElement suggestions: every base class used in the file,
	// and the nested bases that flatten to each flat base.
	bases   map[string]bool
	flatten map[string]map[string]bool
}

// flatBase returns b__e for the nested element b__x__e.
func (c *lintRun) flatBase(nm Name) string {
	return c.l.naming.Format(nm.Block, nm.Elements[len(nm.Elements)-1:], "", "")
}

func (c *lintRun) indexFlat(root *core.Node) {
	c.bases = map[string]bool{}
	c.flatten = map[string]map[string]bool{}
	var walk func(n *core.Node)
	walk = func(n *core.Node) {
		for _, u := range c.classes(n) {
			if u.name.StructErr {
				continue
			}
			c.bases[u.name.Base()] = true
			if len(u.name.Elements) > 1 {
				f := c.flatBase(u.name)
				if c.flatten[f] == nil {
					c.flatten[f] = map[string]bool{}
				}
				c.flatten[f][u.name.Base()] = true
			}
		}
		for _, k := range n.Children {
			walk(k)
		}
	}
	walk(root)
}

// flatSuggestion returns the flat spelling of a nested element, unless it
// would collide with a class already used or with another nested element
// that flattens to the same name.
func (c *lintRun) flatSuggestion(nm Name) string {
	f := c.flatBase(nm)
	if c.bases[f] || len(c.flatten[f]) != 1 {
		return ""
	}
	return c.l.naming.Format(nm.Block, nm.Elements[len(nm.Elements)-1:], nm.Mod, nm.Val)
}

func (c *lintRun) report(r Rule, ref core.ClassRef, suggestion, format string, args ...any) {
	c.diags = append(c.diags, Diagnostic{
		Rule:       r,
		Severity:   c.l.sev[r],
		Range:      ref.Range,
		Class:      ref.Name,
		Message:    fmt.Sprintf(format, args...),
		Suggestion: suggestion,
	})
}

// classes returns the static and conditional classes of n that BEM rules
// apply to, parsed once per node.
func (c *lintRun) classes(n *core.Node) []usedClass {
	if u, ok := c.parsed[n]; ok {
		return u
	}
	var u []usedClass
	for _, ref := range n.Classes {
		if (ref.Kind == core.ClassStatic || ref.Kind == core.ClassConditional) && !c.l.ignored(ref.Name) {
			u = append(u, usedClass{ref: ref, name: c.l.naming.Parse(ref.Name)})
		}
	}
	c.parsed[n] = u
	return u
}

func (c *lintRun) walk(n *core.Node) {
	c.checkNode(n)
	for _, k := range n.Children {
		c.walk(k)
	}
}

func (c *lintRun) checkNode(n *core.Node) {
	l := c.l
	used := c.classes(n)

	var firstBlock string
	for _, u := range used {
		nm, ref := u.name, u.ref

		if !nm.Valid() {
			if l.on(RuleInvalidName) {
				c.report(RuleInvalidName, ref, l.naming.Suggest(nm),
					"%q does not follow the %s naming convention: %v", ref.Name, l.naming.Preset(), nm.Err)
			}
			if nm.StructErr {
				continue // the parts are unreliable
			}
		}

		if l.on(RuleNestedElement) && len(nm.Elements) > 1 {
			c.report(RuleNestedElement, ref, c.flatSuggestion(nm),
				"%q is an element of an element; BEM elements belong directly to the block %q", ref.Name, nm.Block)
		}

		if l.on(RuleModifierWithoutBase) && nm.HasMod && !hasRaw(used, nm.Base()) {
			c.report(RuleModifierWithoutBase, ref, "",
				"modifier %q is used without %q on the same element", ref.Name, nm.Base())
		}

		if l.on(RuleElementOutsideBlock) && nm.IsElement() && !c.insideBlock(n, nm.Block) {
			c.report(RuleElementOutsideBlock, ref, "",
				"element %q is not inside an element with block %q", ref.Name, nm.Block)
		}

		if l.on(RuleMultipleBlocksMix) && !nm.IsElement() {
			switch {
			case firstBlock == "":
				firstBlock = nm.Block
			case nm.Block != firstBlock:
				c.report(RuleMultipleBlocksMix, ref, "",
					"element mixes blocks %q and %q", firstBlock, nm.Block)
			}
		}

		if l.on(RuleMissingInStylesheet) && c.in.Stylesheet != nil && !c.in.Stylesheet.HasClass(ref.Name) {
			c.report(RuleMissingInStylesheet, ref, "", "no selector for %q in the stylesheet", "."+ref.Name)
		}
	}

	if l.on(RuleModuleClassMissing) {
		for _, ref := range n.Classes {
			if ref.Kind != core.ClassModule || l.ignored(ref.Name) {
				continue
			}
			set := c.in.Modules[ref.Module]
			if set == nil || set.HasClass(ref.Name) || set.HasClass(toKebab(ref.Name)) {
				continue
			}
			c.report(RuleModuleClassMissing, ref, "",
				"CSS module %q has no class %q", ref.Module, ref.Name)
		}
	}
}

// insideBlock reports whether an ancestor of n uses block, or whether that
// can't be known because a component sits in between (its markup, which may
// render the block, is in another file). A styled component defined in the
// same file is a known plain element, not a boundary.
func (c *lintRun) insideBlock(n *core.Node, block string) bool {
	for a := n.Parent; a != nil && !a.IsRoot(); a = a.Parent {
		for _, u := range c.classes(a) {
			if !u.name.StructErr && u.name.Block == block {
				return true
			}
		}
		if a.IsComponent() && a.Styled == nil && len(c.classes(a)) == 0 {
			return true
		}
	}
	return false
}

func hasRaw(used []usedClass, raw string) bool {
	for _, u := range used {
		if u.ref.Name == raw {
			return true
		}
	}
	return false
}

// ModulesFor builds Input.Modules for f: for every identifier bound to a
// stylesheet import, load returns that stylesheet's classes. load may return
// nil for a stylesheet it can't read; that identifier is then skipped.
func ModulesFor(f *core.File, load func(core.Import) ClassSet) map[string]ClassSet {
	out := map[string]ClassSet{}
	for _, imp := range f.Stylesheets() {
		for _, id := range []string{imp.Default, imp.Namespace} {
			if id == "" {
				continue
			}
			if set := load(imp); set != nil {
				out[id] = set
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Stylesheet side

// ClassDef is a class selector found in a stylesheet.
type ClassDef struct {
	Name  string
	Range core.Range
}

// UsedClasses records the classes components use, for unusedSelector.
type UsedClasses struct {
	names    map[string]bool
	prefixes []string // known parts of dynamic classes, e.g. "btn--"
}

// CollectUsed gathers the classes used in the given trees. Every kind
// counts; a dynamic class counts as a prefix.
func CollectUsed(roots ...*core.Node) *UsedClasses {
	u := &UsedClasses{names: map[string]bool{}}
	var walk func(n *core.Node)
	walk = func(n *core.Node) {
		for _, c := range n.Classes {
			if c.Kind == core.ClassDynamic {
				u.prefixes = append(u.prefixes, c.Name)
			} else {
				u.names[c.Name] = true
				if c.Kind == core.ClassModule {
					u.names[toKebab(c.Name)] = true
				}
			}
		}
		for _, k := range n.Children {
			walk(k)
		}
	}
	for _, r := range roots {
		if r != nil {
			walk(r)
		}
	}
	return u
}

// Uses reports whether class is used, exactly or through a dynamic prefix.
func (u *UsedClasses) Uses(class string) bool {
	if u.names[class] {
		return true
	}
	for _, p := range u.prefixes {
		if strings.HasPrefix(class, p) {
			return true
		}
	}
	return false
}

// LintStylesheet reports stylesheet classes that are not used.
func (l *Linter) LintStylesheet(defs []ClassDef, used *UsedClasses) []Diagnostic {
	if !l.on(RuleUnusedSelector) || used == nil {
		return nil
	}
	var diags []Diagnostic
	for _, d := range defs {
		if l.ignored(d.Name) || used.Uses(d.Name) {
			continue
		}
		diags = append(diags, Diagnostic{
			Rule:     RuleUnusedSelector,
			Severity: l.sev[RuleUnusedSelector],
			Range:    d.Range,
			Class:    d.Name,
			Message:  fmt.Sprintf("%q is not used by any component", "."+d.Name),
		})
	}
	sortDiagnostics(diags)
	return diags
}

func sortDiagnostics(d []Diagnostic) {
	sort.SliceStable(d, func(i, j int) bool {
		return d[i].Range.Start.Offset < d[j].Range.Start.Offset
	})
}

// Ignored reports whether class matches one of the Ignore patterns.
func (l *Linter) Ignored(class string) bool { return l.ignored(class) }

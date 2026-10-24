package index

import (
	"strings"

	"github.com/TeddyGavi/bem-ls/internal/bem"
	"github.com/TeddyGavi/bem-ls/internal/core"
	"github.com/TeddyGavi/bem-ls/internal/scss"
)

// Location is a range in a file.
type Location struct {
	Path  string
	Range core.Range
}

// Def is a symbol definition and the file it is in.
type Def struct {
	Path   string
	Symbol *scss.Symbol
}

// same reports whether two Defs are the same definition. Symbols are
// compared by position because a re-parsed file has new pointers.
func (d Def) same(o Def) bool {
	return d.Path == o.Path && d.Symbol.Kind == o.Symbol.Kind && d.Symbol.Name == o.Symbol.Name &&
		d.Symbol.Range.Start.Offset == o.Symbol.Range.Start.Offset
}

// Sheets returns the parsed stylesheets of a document: its Sheet, or the
// CSS of each styled-components template in a JSX file.
func (d *Doc) Sheets() []*scss.Sheet {
	if d.Sheet != nil {
		return []*scss.Sheet{d.Sheet}
	}
	return d.StyledSheets
}

// SheetAt returns the stylesheet of d containing offset: d.Sheet, or the
// styled template whose range contains it.
func (d *Doc) SheetAt(offset int) *scss.Sheet {
	if d.Sheet != nil {
		return d.Sheet
	}
	if d.JSX == nil {
		return nil
	}
	for i, def := range d.JSX.Styled {
		if def.Template.Start.Offset <= offset && offset <= def.Template.End.Offset {
			return d.StyledSheets[i]
		}
	}
	return nil
}

// Resolve returns the definitions ref refers to. sheet is the stylesheet
// the reference is in, and path the file containing it. Lookup goes through
// local scopes, @import and "@use ... as *", namespaced @use members and
// @forward chains, then (if enabled) global definitions anywhere in the
// workspace.
func (ix *Index) Resolve(path string, sheet *scss.Sheet, ref *scss.Ref) []Def {
	path = ix.abs(path)
	if ref.Namespace == "" {
		if s := sheet.Lookup(ref); s != nil {
			return []Def{{Path: path, Symbol: s}}
		}
	}
	visited := map[string]bool{path: true}
	var defs []Def
	for _, imp := range sheet.Imports {
		switch {
		case ref.Namespace == "" && imp.Kind == scss.ImportImport,
			ref.Namespace == "" && imp.Kind == scss.ImportUse && imp.Namespace == "*",
			ref.Namespace != "" && imp.Kind == scss.ImportUse && imp.Namespace == ref.Namespace:
			if p, ok := ix.ResolveSass(path, imp.URL); ok {
				defs = append(defs, ix.member(p, ref.Kind, ref.Name, visited)...)
			}
		}
		if len(defs) > 0 {
			return defs
		}
	}
	if ref.Namespace == "" && *ix.cfg.GlobalFallback && ref.Kind != scss.SymCustomProperty {
		defs = ix.globalsNamed(ref.Kind, ref.Name, path)
	}
	if ref.Kind == scss.SymCustomProperty && len(defs) == 0 {
		// Custom properties cascade: any definition in the workspace.
		defs = ix.globalsNamed(ref.Kind, ref.Name, "")
	}
	return defs
}

// member finds a member of the module at path: its own globals, what it
// @imports, and what it @forwards (with prefixes and show/hide applied).
func (ix *Index) member(path string, kind scss.SymbolKind, name string, visited map[string]bool) []Def {
	if visited[path] {
		return nil
	}
	visited[path] = true
	d, err := ix.Get(path)
	if err != nil || d.Sheet == nil {
		return nil
	}
	if s := d.Sheet.Global(kind, name); s != nil {
		return []Def{{Path: path, Symbol: s}}
	}
	for _, imp := range d.Sheet.Imports {
		if imp.Kind == scss.ImportUse {
			continue
		}
		inner := name
		if imp.Kind == scss.ImportForward {
			if imp.Prefix != "" {
				if !strings.HasPrefix(name, imp.Prefix) {
					continue
				}
				inner = strings.TrimPrefix(name, imp.Prefix)
			}
			if !forwards(imp, kind, name, inner) {
				continue
			}
		}
		if p, ok := ix.ResolveSass(path, imp.URL); ok {
			if defs := ix.member(p, kind, inner, visited); len(defs) > 0 {
				return defs
			}
		}
	}
	return nil
}

// forwards applies @forward's show/hide lists, which may name members with
// or without the prefix.
func forwards(imp scss.Import, kind scss.SymbolKind, name, inner string) bool {
	labels := []string{kind.Sigil() + name, kind.Sigil() + inner}
	in := func(list []string) bool {
		for _, l := range labels {
			if contains(list, l) {
				return true
			}
		}
		return false
	}
	if len(imp.Show) > 0 && !in(imp.Show) {
		return false
	}
	return !in(imp.Hide)
}

// globalsNamed returns every global symbol of kind and name in loaded
// stylesheets (and styled templates), except in skip.
func (ix *Index) globalsNamed(kind scss.SymbolKind, name, skip string) []Def {
	var defs []Def
	for _, d := range ix.Docs() {
		if d.Path == skip {
			continue
		}
		for _, sh := range d.Sheets() {
			for _, s := range sh.Symbols {
				if s.Kind == kind && s.Name == name && s.Scope == nil {
					defs = append(defs, Def{Path: d.Path, Symbol: s})
				}
			}
		}
	}
	return defs
}

// References returns every reference in the loaded documents that resolves
// to def.
func (ix *Index) References(def Def) []Location {
	def.Path = ix.abs(def.Path)
	var out []Location
	for _, d := range ix.Docs() {
		for _, sh := range d.Sheets() {
			for _, r := range sh.Refs {
				if r.Kind != def.Symbol.Kind || !strings.HasSuffix(r.Name, def.Symbol.Name) {
					continue
				}
				for _, got := range ix.Resolve(d.Path, sh, r) {
					if got.same(def) {
						out = append(out, Location{Path: d.Path, Range: r.Range})
						break
					}
				}
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Classes

// Closure returns paths plus every stylesheet they reach through @use,
// @forward and @import, in discovery order.
func (ix *Index) Closure(paths ...string) []string {
	var out []string
	seen := map[string]bool{}
	var visit func(p string)
	visit = func(p string) {
		p = ix.abs(p)
		if seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
		d, err := ix.Get(p)
		if err != nil || d.Sheet == nil {
			return
		}
		for _, imp := range d.Sheet.Imports {
			if dep, ok := ix.ResolveSass(p, imp.URL); ok {
				visit(dep)
			}
		}
	}
	for _, p := range paths {
		visit(p)
	}
	return out
}

// ClassSet is the union of the classes of some stylesheets. It implements
// bem.ClassSet.
type ClassSet struct {
	Paths  []string
	sheets []*scss.Sheet
}

// HasClass implements bem.ClassSet.
func (c *ClassSet) HasClass(name string) bool {
	for _, s := range c.sheets {
		if s.HasClass(name) {
			return true
		}
	}
	return false
}

func (ix *Index) classSet(paths []string) *ClassSet {
	cs := &ClassSet{}
	for _, p := range ix.Closure(paths...) {
		if d, err := ix.Get(p); err == nil && d.Sheet != nil {
			cs.Paths = append(cs.Paths, p)
			cs.sheets = append(cs.sheets, d.Sheet)
		}
	}
	return cs
}

// moduleSheet resolves a CSS-module identifier used in a JSX file to the
// module's path.
func (ix *Index) moduleSheet(f *core.File, ident string) (string, bool) {
	imp, ok := f.ModuleImport(ident)
	if !ok {
		return "", false
	}
	return ix.resolveJSImport(f.Name, imp.Source, true)
}

// LintInput builds the input for bem.Linter.Lint for a JSX file: its tree,
// the classes of its primary stylesheets, and its CSS modules. Stylesheet
// is nil when no stylesheet was found (so missingInStylesheet stays quiet)
// or when one of them can't be parsed (.sass).
func (ix *Index) LintInput(jsxPath string) (bem.Input, error) {
	jsxPath = ix.abs(jsxPath)
	d, err := ix.Get(jsxPath)
	if err != nil {
		return bem.Input{}, err
	}
	in := bem.Input{Root: d.JSX.Root}
	if primary := ix.PrimaryStylesheets(jsxPath); len(primary) > 0 {
		cs := ix.classSet(primary)
		if len(cs.Paths) == len(ix.Closure(primary...)) {
			in.Stylesheet = cs
		}
	}
	in.Modules = bem.ModulesFor(d.JSX, func(imp core.Import) bem.ClassSet {
		p, ok := ix.resolveJSImport(jsxPath, imp.Source, true)
		if !ok {
			return nil
		}
		cs := ix.classSet([]string{p})
		if len(cs.Paths) == 0 {
			return nil
		}
		return cs
	})
	return in, nil
}

// UsedClasses returns the classes used by every component linked to the
// stylesheet, directly or through @use/@import, for bem.LintStylesheet.
func (ix *Index) UsedClasses(sheetPath string) *bem.UsedClasses {
	var roots []*core.Node
	for _, c := range ix.ComponentsUsing(sheetPath) {
		if d, err := ix.Get(c); err == nil && d.JSX != nil {
			roots = append(roots, d.JSX.Root)
		}
	}
	return bem.CollectUsed(roots...)
}

// ComponentsUsing returns the loaded components whose stylesheets (primary
// ones and imported ones, with their @use/@import closure) include
// sheetPath.
func (ix *Index) ComponentsUsing(sheetPath string) []string {
	sheetPath = ix.abs(sheetPath)
	var out []string
	for _, d := range ix.Docs() {
		if d.Kind == KindJSX && ix.componentSheets(d)[sheetPath] {
			out = append(out, d.Path)
		}
	}
	return out
}

// componentSheets returns every stylesheet a component reaches, cached
// until the component or any stylesheet changes.
func (ix *Index) componentSheets(d *Doc) map[string]bool {
	ix.mu.RLock()
	e, ok := ix.closures[d.Path]
	gen, sheetGen := ix.gen, ix.sheetGen
	ix.mu.RUnlock()
	if ok && e.doc == d && e.gen == gen && e.sheetGen == sheetGen {
		return e.sheets
	}
	roots := append([]string(nil), ix.PrimaryStylesheets(d.Path)...)
	for _, imp := range d.JSX.Stylesheets() {
		if p, ok := ix.resolveJSImport(d.Path, imp.Source, true); ok {
			roots = append(roots, p)
		}
	}
	sheets := map[string]bool{}
	for _, p := range ix.Closure(roots...) {
		sheets[p] = true
	}
	ix.mu.Lock()
	// Only store if nothing changed meanwhile; otherwise the next call
	// recomputes.
	if ix.gen == gen && ix.sheetGen == sheetGen {
		ix.closures[d.Path] = closureEntry{doc: d, gen: gen, sheetGen: sheetGen, sheets: sheets}
	}
	ix.mu.Unlock()
	return sheets
}

// ClassDefinitions returns where a class used in a JSX file is defined. A
// module reference (ref.Kind == core.ClassModule) is looked up in its CSS
// module, matching .name and, for camelCase names, .kebab-name.
func (ix *Index) ClassDefinitions(jsxPath string, ref core.ClassRef) []Location {
	jsxPath = ix.abs(jsxPath)
	d, err := ix.Get(jsxPath)
	if err != nil || d.JSX == nil {
		return nil
	}
	var paths []string
	names := []string{ref.Name}
	if ref.Kind == core.ClassModule {
		p, ok := ix.moduleSheet(d.JSX, ref.Module)
		if !ok {
			return nil
		}
		paths = []string{p}
		if k := kebab(ref.Name); k != ref.Name {
			names = append(names, k)
		}
	} else {
		paths = ix.PrimaryStylesheets(jsxPath)
		for _, imp := range d.JSX.Stylesheets() {
			if imp.IsModule() {
				continue
			}
			if p, ok := ix.resolveJSImport(jsxPath, imp.Source, true); ok && !contains(paths, p) {
				paths = append(paths, p)
			}
		}
	}
	var out []Location
	for _, p := range ix.Closure(paths...) {
		sd, err := ix.Get(p)
		if err != nil || sd.Sheet == nil {
			continue
		}
		for _, n := range names {
			for _, c := range sd.Sheet.ClassDefs(n) {
				out = append(out, Location{Path: p, Range: c.Range})
			}
		}
	}
	return out
}

// ClassUsages returns where components use a class defined in a
// stylesheet: plain class names in components linked to it, and
// styles.name / styles['name'] in components importing it as a module.
func (ix *Index) ClassUsages(sheetPath, class string) []Location {
	sheetPath = ix.abs(sheetPath)
	var out []Location
	for _, c := range ix.ComponentsUsing(sheetPath) {
		d, err := ix.Get(c)
		if err != nil || d.JSX == nil {
			continue
		}
		moduleIdents := map[string]bool{}
		for _, imp := range d.JSX.Stylesheets() {
			if p, ok := ix.resolveJSImport(c, imp.Source, true); ok && contains(ix.Closure(p), sheetPath) {
				for _, id := range []string{imp.Default, imp.Namespace} {
					if id != "" {
						moduleIdents[id] = true
					}
				}
			}
		}
		walkClasses(d.JSX.Root, func(r core.ClassRef) {
			switch r.Kind {
			case core.ClassStatic, core.ClassConditional:
				if r.Name == class && !isModulePath(sheetPath) {
					out = append(out, Location{Path: c, Range: r.Range})
				}
			case core.ClassModule:
				if moduleIdents[r.Module] && (r.Name == class || kebab(r.Name) == class) {
					out = append(out, Location{Path: c, Range: r.Range})
				}
			}
		})
	}
	return out
}

func walkClasses(n *core.Node, fn func(core.ClassRef)) {
	for _, c := range n.Classes {
		fn(c)
	}
	for _, k := range n.Children {
		walkClasses(k, fn)
	}
}

// ResolveStyled finds the styled-components definition rendered by a JSX
// tag in a file: defined in the same file, imported by name
// (import { Button } from './Button.styles'), or through a namespace
// (import * as S ...; <S.Button>).
func (ix *Index) ResolveStyled(jsxPath, tag string) (string, *core.StyledDef) {
	jsxPath = ix.abs(jsxPath)
	d, err := ix.Get(jsxPath)
	if err != nil || d.JSX == nil {
		return "", nil
	}
	if def := d.JSX.StyledByName(tag); def != nil {
		return jsxPath, def
	}
	ns, member, dotted := strings.Cut(tag, ".")
	for _, imp := range d.JSX.Imports {
		exported := ""
		switch {
		case dotted && imp.Namespace == ns:
			exported = member
		case !dotted:
			for _, n := range imp.Named {
				if n.Local == tag {
					exported = n.Name
				}
			}
		}
		if exported == "" {
			continue
		}
		p, ok := ix.resolveJSImport(jsxPath, imp.Source, false)
		if !ok {
			continue
		}
		if td, err := ix.Get(p); err == nil && td.JSX != nil {
			if def := td.JSX.StyledByName(exported); def != nil {
				return p, def
			}
		}
	}
	return "", nil
}

// StylesheetLintInput returns what bem.Linter.LintStylesheet needs for a
// stylesheet: its class definitions and the classes components use. ok is
// false when no component uses the stylesheet, in which case "unused" can't
// be judged.
func (ix *Index) StylesheetLintInput(sheetPath string) (defs []bem.ClassDef, used *bem.UsedClasses, ok bool) {
	d, err := ix.Get(sheetPath)
	if err != nil || d.Sheet == nil || len(ix.ComponentsUsing(sheetPath)) == 0 {
		return nil, nil, false
	}
	for _, c := range d.Sheet.Classes {
		defs = append(defs, bem.ClassDef{Name: c.Name, Range: c.Range})
	}
	return defs, ix.UsedClasses(sheetPath), true
}

// ClassInfo is a class definition with the file it is in.
type ClassInfo struct {
	Path string
	Def  scss.ClassDef
}

// AvailableClasses returns the class definitions a component can use: those
// in its primary and imported (non-module) stylesheets and everything they
// reach through @use/@import.
func (ix *Index) AvailableClasses(jsxPath string) []ClassInfo {
	jsxPath = ix.abs(jsxPath)
	d, err := ix.Get(jsxPath)
	if err != nil || d.JSX == nil {
		return nil
	}
	paths := append([]string(nil), ix.PrimaryStylesheets(jsxPath)...)
	for _, imp := range d.JSX.Stylesheets() {
		if imp.IsModule() {
			continue
		}
		if p, ok := ix.resolveJSImport(jsxPath, imp.Source, true); ok && !contains(paths, p) {
			paths = append(paths, p)
		}
	}
	return ix.classesIn(paths)
}

// ModuleClasses returns the class definitions of the CSS module bound to
// ident in a component, with the module's @use/@import closure.
func (ix *Index) ModuleClasses(jsxPath, ident string) []ClassInfo {
	jsxPath = ix.abs(jsxPath)
	d, err := ix.Get(jsxPath)
	if err != nil || d.JSX == nil {
		return nil
	}
	p, ok := ix.moduleSheet(d.JSX, ident)
	if !ok {
		return nil
	}
	return ix.classesIn([]string{p})
}

func (ix *Index) classesIn(paths []string) []ClassInfo {
	var out []ClassInfo
	for _, p := range ix.Closure(paths...) {
		if sd, err := ix.Get(p); err == nil && sd.Sheet != nil {
			for _, c := range sd.Sheet.Classes {
				out = append(out, ClassInfo{Path: p, Def: c})
			}
		}
	}
	return out
}

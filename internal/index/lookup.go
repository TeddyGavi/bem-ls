package index

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Link is a stylesheet found for a component, and the strategy that found
// it.
type Link struct {
	Path     string
	Strategy string
}

// StylesheetsFor returns the stylesheet candidates for a JSX/TSX file, in
// the configured lookup order, without duplicates.
func (ix *Index) StylesheetsFor(jsxPath string) []Link {
	jsxPath = ix.abs(jsxPath)
	d, err := ix.Get(jsxPath)
	if err != nil || d.Kind != KindJSX {
		return nil
	}
	var out []Link
	seen := map[string]bool{}
	add := func(p, strategy string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, Link{Path: p, Strategy: strategy})
		}
	}
	dir := filepath.Dir(jsxPath)
	base := componentBase(jsxPath)

	for _, strategy := range ix.cfg.Lookup {
		switch strategy {
		case LookupImport:
			for _, imp := range d.JSX.Stylesheets() {
				if p, ok := ix.resolveJSImport(jsxPath, imp.Source, true); ok {
					add(p, LookupImport)
				}
			}
		case LookupSibling:
			for _, p := range ix.matchBase(dir, base) {
				add(p, LookupSibling)
			}
		case LookupDirectory:
			for _, p := range ix.unclaimedStylesheets(dir) {
				add(p, LookupDirectory)
			}
		case LookupParent:
			up := dir
			for i := 0; i < ix.cfg.MaxSearchDepth; i++ {
				parent := filepath.Dir(up)
				if parent == up || !strings.HasPrefix(parent, ix.root) {
					break
				}
				up = parent
				for _, p := range ix.matchBase(up, base) {
					add(p, LookupParent)
				}
				for _, p := range ix.matchBase(filepath.Join(up, "styles"), base) {
					add(p, LookupParent)
				}
			}
		}
	}
	return out
}

// PrimaryStylesheets returns the stylesheets a component's classes are
// checked against: every imported stylesheet that isn't a CSS module, or,
// if there are none, the first candidate any other strategy found. CSS
// modules are checked separately, through their identifiers.
func (ix *Index) PrimaryStylesheets(jsxPath string) []string {
	jsxPath = ix.abs(jsxPath)
	d, err := ix.Get(jsxPath)
	if err != nil || d.Kind != KindJSX {
		return nil
	}
	ix.mu.RLock()
	e, ok := ix.links[jsxPath]
	gen := ix.gen
	ix.mu.RUnlock()
	if ok && e.gen == gen && e.doc == d {
		return e.primary
	}

	links := ix.StylesheetsFor(jsxPath)
	moduleImports := map[string]bool{}
	for _, imp := range d.JSX.Stylesheets() {
		if imp.IsModule() {
			if p, ok := ix.resolveJSImport(jsxPath, imp.Source, true); ok {
				moduleImports[p] = true
			}
		}
	}
	var primary []string
	for _, l := range links {
		if l.Strategy == LookupImport && !moduleImports[l.Path] {
			primary = append(primary, l.Path)
		}
	}
	if len(primary) == 0 {
		for _, l := range links {
			if l.Strategy != LookupImport && !isModulePath(l.Path) {
				primary = append(primary, l.Path)
				break
			}
		}
	}
	sheets := make([]string, len(links))
	for i, l := range links {
		sheets[i] = l.Path
	}
	ix.mu.Lock()
	ix.links[jsxPath] = linkEntry{gen: gen, doc: d, sheets: sheets, primary: primary}
	ix.mu.Unlock()
	return primary
}

func isModulePath(p string) bool { return strings.Contains(filepath.Base(p), ".module.") }

// componentBase returns "Card" for ".../Card.tsx" and ".../Card.test.tsx"
// is not special-cased; "index" components use their directory name.
func componentBase(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if strings.EqualFold(base, "index") {
		return filepath.Base(filepath.Dir(path))
	}
	return base
}

// matchBase returns the stylesheets in dir named base+ext for each
// configured extension, in extension order. Names match case-insensitively
// and also in kebab-case (MyCard matches my-card.scss).
func (ix *Index) matchBase(dir, base string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := []string{strings.ToLower(base), kebab(base)}
	var out []string
	for _, ext := range ix.cfg.Extensions {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			n := strings.ToLower(e.Name())
			for _, want := range names {
				if n == want+ext || n == "_"+want+ext {
					out = append(out, filepath.Join(dir, e.Name()))
				}
			}
		}
	}
	return dedupe(out)
}

// unclaimedStylesheets returns the stylesheets in dir that aren't named
// after some other component in dir. Card.tsx alone in a folder with
// styles.scss gets it; it doesn't get Button.scss next to Button.tsx.
func (ix *Index) unclaimedStylesheets(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	components := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() && KindOf(e.Name()) == KindJSX {
			b := strings.ToLower(componentBase(filepath.Join(dir, e.Name())))
			components[b] = true
			components[kebab(componentBase(filepath.Join(dir, e.Name())))] = true
		}
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || KindOf(e.Name()) != KindStylesheet {
			continue
		}
		stem := strings.ToLower(e.Name())
		stem = strings.TrimPrefix(stem, "_")
		if i := strings.IndexByte(stem, '.'); i > 0 {
			stem = stem[:i]
		}
		if !components[stem] {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

// kebab turns "MyCard" into "my-card".
func kebab(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		if unicode.IsUpper(r) {
			if i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1])) {
				b.WriteByte('-')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Module resolution

var jsResolveExts = []string{".tsx", ".ts", ".jsx", ".js", ".mjs", ".cjs"}

// resolveJSImport resolves a JS import specifier from a file: relative
// ("./x"), root-absolute ("/src/x") or aliased ("@/x"). Package imports
// are not resolved. stylesheet selects exact-file resolution for
// stylesheet imports; otherwise JS extensions and index files are tried.
func (ix *Index) resolveJSImport(from, spec string, stylesheet bool) (string, bool) {
	spec = stripQuery(spec)
	var base string
	switch {
	case strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") || spec == "." || spec == "..":
		base = filepath.Join(filepath.Dir(from), filepath.FromSlash(spec))
	case strings.HasPrefix(spec, "/"):
		base = filepath.Join(ix.root, filepath.FromSlash(spec))
	default:
		matched := false
		for prefix, target := range ix.cfg.Aliases {
			if strings.HasPrefix(spec, prefix) {
				base = filepath.Join(ix.root, filepath.FromSlash(target), filepath.FromSlash(strings.TrimPrefix(spec, prefix)))
				matched = true
				break
			}
		}
		if !matched {
			return "", false
		}
	}
	if stylesheet {
		return base, isFile(base)
	}
	if isFile(base) {
		return base, true
	}
	for _, ext := range jsResolveExts {
		if isFile(base + ext) {
			return base + ext, true
		}
	}
	for _, ext := range jsResolveExts {
		if p := filepath.Join(base, "index"+ext); isFile(p) {
			return p, true
		}
	}
	return "", false
}

// ResolveSass resolves a @use/@forward/@import URL the way Sass does:
// relative to the file, then each load path; trying partials (_x), the
// .scss, .sass and .css extensions, and index files.
func (ix *Index) ResolveSass(from, url string) (string, bool) {
	if strings.HasPrefix(url, "sass:") || strings.Contains(url, "://") || strings.HasPrefix(url, "//") {
		return "", false
	}
	url = filepath.FromSlash(strings.TrimPrefix(url, "~"))
	dirs := []string{filepath.Dir(ix.abs(from))}
	for _, lp := range ix.cfg.LoadPaths {
		dirs = append(dirs, filepath.Join(ix.root, filepath.FromSlash(lp)))
	}
	dirs = append(dirs, filepath.Join(ix.root, "node_modules"))
	for _, dir := range dirs {
		if p, ok := sassCandidates(filepath.Join(dir, url)); ok {
			return p, true
		}
	}
	return "", false
}

func sassCandidates(base string) (string, bool) {
	dir, name := filepath.Split(base)
	var tries []string
	if ext := strings.ToLower(filepath.Ext(name)); ext == ".scss" || ext == ".sass" || ext == ".css" {
		tries = []string{name, "_" + name}
	} else {
		for _, ext := range []string{".scss", ".sass", ".css"} {
			tries = append(tries, name+ext, "_"+name+ext)
		}
		for _, ext := range []string{".scss", ".sass", ".css"} {
			tries = append(tries, filepath.Join(name, "index"+ext), filepath.Join(name, "_index"+ext))
		}
	}
	for _, t := range tries {
		if p := filepath.Join(dir, t); isFile(p) {
			return p, true
		}
	}
	return "", false
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func stripQuery(s string) string {
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		return s[:i]
	}
	return s
}

// ResolveStylesheetImport resolves a stylesheet import in a JSX file
// (import './Card.scss') to a path.
func (ix *Index) ResolveStylesheetImport(from, spec string) (string, bool) {
	return ix.resolveJSImport(ix.abs(from), spec, true)
}

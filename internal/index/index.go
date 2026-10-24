// Package index keeps a parsed view of a workspace: JSX/TSX components,
// their stylesheets, and the @use/@import graph between stylesheets.
//
// Documents are parsed lazily and cached: editor buffers by version, files on
// disk by modification time and size. Scan loads a whole workspace with a
// bounded pool of workers. All methods are safe for concurrent use; a Doc is
// immutable once returned.
package index

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TeddyGavi/bem-ls/internal/core"
	"github.com/TeddyGavi/bem-ls/internal/scss"
)

// Config controls the index. The zero value gives the defaults.
type Config struct {
	// Lookup is the order of strategies for finding a component's
	// stylesheet: "import", "sibling", "directory", "parent".
	Lookup []string `json:"lookup,omitempty"`
	// Extensions are the stylesheet suffixes tried, in order, for sibling
	// and parent lookups.
	Extensions []string `json:"extensions,omitempty"`
	// MaxSearchDepth is how many parent directories "parent" looks in.
	MaxSearchDepth int `json:"maxSearchDepth,omitempty"`
	// LoadPaths are directories, relative to the root, where Sass also
	// looks for @use/@import URLs.
	LoadPaths []string `json:"loadPaths,omitempty"`
	// Aliases map import prefixes to root-relative directories, e.g.
	// {"@/": "src/"}.
	Aliases map[string]string `json:"aliases,omitempty"`
	// Exclude lists directory names Scan skips.
	Exclude []string `json:"exclude,omitempty"`
	// MaxFileSize skips larger files (bytes).
	MaxFileSize int64 `json:"maxFileSize,omitempty"`
	// Workers bounds Scan's concurrency; 0 means the number of CPUs.
	Workers int `json:"workers,omitempty"`
	// GlobalFallback resolves a variable, mixin, function or placeholder
	// that isn't reachable through @use/@import to a global definition
	// anywhere in the workspace. This covers build setups that inject
	// shared partials.
	GlobalFallback *bool `json:"globalFallback,omitempty"`
	// ConfigureParser, if set, adjusts the JSX parser (class attributes,
	// module identifiers, styled-components factories).
	ConfigureParser func(*core.Parser) `json:"-"`
}

// Strategy names for Config.Lookup.
const (
	LookupImport    = "import"
	LookupSibling   = "sibling"
	LookupDirectory = "directory"
	LookupParent    = "parent"
)

func (c Config) withDefaults() Config {
	if len(c.Lookup) == 0 {
		c.Lookup = []string{LookupImport, LookupSibling, LookupDirectory, LookupParent}
	}
	if len(c.Extensions) == 0 {
		c.Extensions = []string{".module.scss", ".scss", ".module.css", ".css", ".module.sass", ".sass"}
	}
	if c.MaxSearchDepth == 0 {
		c.MaxSearchDepth = 2
	}
	if len(c.Exclude) == 0 {
		c.Exclude = []string{"node_modules", ".git", "dist", "build", "out", "coverage", ".next", ".turbo", "vendor"}
	}
	if c.MaxFileSize == 0 {
		c.MaxFileSize = 1 << 20
	}
	if c.Workers <= 0 {
		c.Workers = runtime.NumCPU()
	}
	if c.GlobalFallback == nil {
		t := true
		c.GlobalFallback = &t
	}
	return c
}

// Validate reports configuration mistakes.
func (c Config) Validate() error {
	for _, s := range c.Lookup {
		switch s {
		case LookupImport, LookupSibling, LookupDirectory, LookupParent:
		default:
			return fmt.Errorf("unknown stylesheet lookup %q (want import, sibling, directory or parent)", s)
		}
	}
	for _, e := range c.Extensions {
		if !strings.HasPrefix(e, ".") {
			return fmt.Errorf("stylesheet extension %q must start with '.'", e)
		}
	}
	if c.MaxSearchDepth < 0 {
		return errors.New("maxSearchDepth must not be negative")
	}
	return nil
}

// Kind is the kind of a document.
type Kind int

const (
	KindOther Kind = iota
	KindJSX
	KindStylesheet
)

var (
	jsxExts   = []string{".tsx", ".jsx", ".ts", ".js", ".mts", ".mjs", ".cts", ".cjs"}
	sheetExts = []string{".scss", ".css", ".sass"}
)

// KindOf classifies a path by extension.
func KindOf(path string) Kind {
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case contains(jsxExts, ext) && !strings.HasSuffix(path, ".d.ts"):
		return KindJSX
	case contains(sheetExts, ext):
		return KindStylesheet
	}
	return KindOther
}

// Doc is a parsed document. It must not be modified.
type Doc struct {
	Path    string
	Kind    Kind
	Version int // editor version, or -1 when read from disk
	Text    string

	// For KindJSX:
	JSX    *core.File
	JSXErr error
	// StyledSheets[i] is the CSS of JSX.Styled[i], parsed at its position
	// in this file.
	StyledSheets []*scss.Sheet

	// For KindStylesheet. Nil for indented .sass, which isn't parsed yet.
	Sheet *scss.Sheet

	modTime time.Time
	size    int64
}

// Unsupported reports whether the document's syntax isn't parsed (.sass).
func (d *Doc) Unsupported() bool { return d.Kind == KindStylesheet && d.Sheet == nil }

type overlay struct {
	version int
	text    string
}

// Index is a workspace index.
type Index struct {
	root string
	cfg  Config

	mu    sync.RWMutex
	docs  map[string]*Doc
	open  map[string]overlay
	links map[string]linkEntry // JSX path -> stylesheet candidates
	gen   int                  // bumped when stylesheets appear or disappear
	// sheetGen is bumped whenever any stylesheet is (re)parsed or removed,
	// which may change @use/@import graphs.
	sheetGen int
	closures map[string]closureEntry // JSX path -> reachable stylesheets
}

type closureEntry struct {
	doc           *Doc
	gen, sheetGen int
	sheets        map[string]bool
}

type linkEntry struct {
	gen     int
	doc     *Doc
	sheets  []string
	primary []string
}

// New returns an index for the workspace rooted at root.
func New(root string, cfg Config) (*Index, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &Index{
		root:     abs,
		cfg:      cfg.withDefaults(),
		docs:     map[string]*Doc{},
		open:     map[string]overlay{},
		links:    map[string]linkEntry{},
		closures: map[string]closureEntry{},
	}, nil
}

// Root returns the workspace root.
func (ix *Index) Root() string { return ix.root }

// Config returns the effective configuration.
func (ix *Index) Config() Config { return ix.cfg }

func (ix *Index) abs(path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(ix.root, path)
	}
	return filepath.Clean(path)
}

// Open records an editor buffer and parses it. Later Opens or Changes with
// an older version are ignored.
func (ix *Index) Open(path string, version int, text string) *Doc {
	path = ix.abs(path)
	ix.mu.Lock()
	if ov, ok := ix.open[path]; ok && ov.version > version {
		d := ix.docs[path]
		ix.mu.Unlock()
		return d
	}
	_, existed := ix.docs[path]
	ix.open[path] = overlay{version: version, text: text}
	ix.mu.Unlock()

	d := ix.parse(path, text, version, time.Time{}, int64(len(text)))
	ix.mu.Lock()
	if ov := ix.open[path]; ov.version == version {
		ix.docs[path] = d
	}
	if d.Kind == KindStylesheet {
		ix.sheetGen++
		if !existed {
			ix.gen++
		}
	}
	ix.mu.Unlock()
	return d
}

// Change is Open for a new version of an open buffer.
func (ix *Index) Change(path string, version int, text string) *Doc {
	return ix.Open(path, version, text)
}

// Close forgets an editor buffer; the file on disk is used again.
func (ix *Index) Close(path string) {
	path = ix.abs(path)
	ix.mu.Lock()
	delete(ix.open, path)
	delete(ix.docs, path)
	if KindOf(path) == KindStylesheet {
		ix.sheetGen++
	}
	ix.mu.Unlock()
}

// Invalidate drops the cached copy of a file changed or deleted on disk.
func (ix *Index) Invalidate(path string) {
	path = ix.abs(path)
	ix.mu.Lock()
	if _, isOpen := ix.open[path]; !isOpen {
		delete(ix.docs, path)
	}
	if KindOf(path) == KindStylesheet {
		ix.gen++
		ix.sheetGen++
	}
	ix.mu.Unlock()
}

// Get returns the document at path: the editor buffer if it is open,
// otherwise the file on disk, re-read if it changed.
func (ix *Index) Get(path string) (*Doc, error) {
	path = ix.abs(path)
	ix.mu.RLock()
	d, cached := ix.docs[path]
	_, isOpen := ix.open[path]
	ix.mu.RUnlock()
	if isOpen && cached {
		return d, nil
	}

	fi, err := os.Stat(path)
	if err != nil {
		if cached {
			ix.mu.Lock()
			delete(ix.docs, path)
			ix.gen++
			ix.sheetGen++
			ix.mu.Unlock()
		}
		return nil, err
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	if cached && d.Version < 0 && d.modTime.Equal(fi.ModTime()) && d.size == fi.Size() {
		return d, nil
	}
	if fi.Size() > ix.cfg.MaxFileSize {
		return nil, fmt.Errorf("%s: larger than %d bytes", path, ix.cfg.MaxFileSize)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d = ix.parse(path, string(b), -1, fi.ModTime(), fi.Size())

	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, nowOpen := ix.open[path]; nowOpen {
		if cur, ok := ix.docs[path]; ok {
			return cur, nil
		}
	}
	if d.Kind == KindStylesheet {
		ix.sheetGen++
		if !cached {
			ix.gen++
		}
	}
	ix.docs[path] = d
	return d, nil
}

// Cached returns the document at path only if it is already loaded.
func (ix *Index) Cached(path string) *Doc {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.docs[ix.abs(path)]
}

// Docs returns the loaded documents, sorted by path.
func (ix *Index) Docs() []*Doc {
	ix.mu.RLock()
	out := make([]*Doc, 0, len(ix.docs))
	for _, d := range ix.docs {
		out = append(out, d)
	}
	ix.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func (ix *Index) parse(path, text string, version int, mod time.Time, size int64) *Doc {
	d := &Doc{Path: path, Kind: KindOf(path), Version: version, Text: text, modTime: mod, size: size}
	switch d.Kind {
	case KindJSX:
		d.JSX, d.JSXErr = core.ParseFileWith(path, text, ix.cfg.ConfigureParser)
		for _, def := range d.JSX.Styled {
			d.StyledSheets = append(d.StyledSheets, scss.ParseAt(path, d.JSX.TemplateCSS(def), def.Template.Start))
		}
	case KindStylesheet:
		if strings.EqualFold(filepath.Ext(path), ".sass") {
			break // indented syntax: not supported yet
		}
		d.Sheet = scss.Parse(path, text)
	}
	return d
}

// ScanStats summarises a Scan.
type ScanStats struct {
	Files    int
	Failed   int
	Duration time.Duration
}

// Scan loads every JSX/TSX and stylesheet file under the root, skipping
// excluded directories, with at most Config.Workers files parsed at once.
// It stops early if ctx is cancelled.
func (ix *Index) Scan(ctx context.Context) (ScanStats, error) {
	start := time.Now()
	paths := make(chan string)
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		stats  ScanStats
		walkFn = func(path string, de fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable entries are skipped
			}
			if de.IsDir() {
				if path != ix.root && (contains(ix.cfg.Exclude, de.Name()) || strings.HasPrefix(de.Name(), ".")) {
					return filepath.SkipDir
				}
				return nil
			}
			if KindOf(path) == KindOther {
				return nil
			}
			select {
			case paths <- path:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	)
	for i := 0; i < ix.cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range paths {
				_, err := ix.Get(p)
				mu.Lock()
				stats.Files++
				if err != nil {
					stats.Failed++
				}
				mu.Unlock()
			}
		}()
	}
	err := filepath.WalkDir(ix.root, walkFn)
	close(paths)
	wg.Wait()
	stats.Duration = time.Since(start)
	return stats, err
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

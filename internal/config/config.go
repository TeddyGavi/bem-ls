// Package config defines the settings shared by the language server and
// the CLI, and loads them from bem.config.json.
//
// Settings come from the editor (initializationOptions, then
// workspace/didChangeConfiguration) and from a bem.config.json file at the
// project root. The file wins: its values are deep-merged over the editor's.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/TeddyGavi/bem-ls/internal/bem"
	"github.com/TeddyGavi/bem-ls/internal/generator"
)

// FileName is the project configuration file.
const FileName = "bem.config.json"

// Settings is the configuration. JSON names are the same in
// bem.config.json, editor settings (under "bem") and initializationOptions.
type Settings struct {
	// Schema lets bem.config.json point at its JSON schema.
	Schema string `json:"$schema,omitempty"`

	Naming bem.NamingConfig `json:"naming"`
	// Rules maps rule names to "off", "hint", "info", "warning" or "error".
	Rules map[bem.Rule]bem.Severity `json:"rules,omitempty"`
	// Ignore holds regular expressions; matching classes are skipped by
	// every rule and by skeleton generation.
	Ignore []string `json:"ignore,omitempty"`

	Stylesheet StylesheetSettings `json:"stylesheet"`
	Generator  GeneratorSettings  `json:"generator"`
	JSX        JSXSettings        `json:"jsx"`
	CSSModules CSSModulesSettings `json:"cssModules"`
	Styled     StyledSettings     `json:"styledComponents"`
	GhostText  GhostTextSettings  `json:"ghostText"`
	Misc       MiscSettings       `json:"misc"`
}

// StylesheetSettings controls how components are paired with stylesheets
// and how Sass URLs resolve.
type StylesheetSettings struct {
	Lookup         []string          `json:"lookup,omitempty"`
	Extensions     []string          `json:"extensions,omitempty"`
	MaxSearchDepth int               `json:"maxSearchDepth,omitempty"`
	LoadPaths      []string          `json:"loadPaths,omitempty"`
	Aliases        map[string]string `json:"aliases,omitempty"`
	Exclude        []string          `json:"exclude,omitempty"`
	GlobalFallback *bool             `json:"globalFallback,omitempty"`
}

// GeneratorSettings controls the generated SCSS.
type GeneratorSettings struct {
	// Indent is a number of spaces or "tab"; default 2.
	Indent  Indent `json:"indent,omitempty"`
	Nesting string `json:"nesting,omitempty"` // "dom" or "bem"
}

// Indent accepts 2, "2" or "tab".
type Indent string

// UnmarshalJSON implements json.Unmarshaler.
func (i *Indent) UnmarshalJSON(b []byte) error {
	var n int
	if err := json.Unmarshal(b, &n); err == nil {
		*i = Indent(strconv.Itoa(n))
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return errors.New(`indent must be a number or "tab"`)
	}
	*i = Indent(s)
	return nil
}

// JSXSettings controls how class names are read from JSX.
type JSXSettings struct {
	ClassAttributes []string `json:"classAttributes,omitempty"`
	ClassFunctions  []string `json:"classFunctions,omitempty"`
}

// CSSModulesSettings controls CSS-modules detection.
type CSSModulesSettings struct {
	// Identifiers are treated as CSS modules even without an import.
	Identifiers []string `json:"identifiers,omitempty"`
}

// StyledSettings controls styled-components detection.
type StyledSettings struct {
	Identifiers []string `json:"identifiers,omitempty"`
}

// GhostTextSettings controls inline suggestions in stylesheets.
type GhostTextSettings struct {
	Enabled *bool `json:"enabled,omitempty"`
}

// MiscSettings holds the rest.
type MiscSettings struct {
	// DiagnosticsDelay is the debounce in milliseconds after a change.
	DiagnosticsDelay int `json:"diagnosticsDelay,omitempty"`
	// ScanOnStartup indexes the workspace in the background; default true.
	ScanOnStartup *bool `json:"scanOnStartup,omitempty"`
}

// Parse decodes settings, accepted at the top level or under a "bem" key.
// Empty input gives the zero value (all defaults). With strict set,
// unknown fields are errors, which catches typos in bem.config.json;
// editors send extra keys of their own, so their settings aren't strict.
func Parse(raw json.RawMessage, strict bool) (Settings, error) {
	var s Settings
	raw = bytes.TrimSpace(raw)
	if isEmpty(raw) {
		return s, nil
	}
	if inner, ok := unwrap(raw); ok {
		raw = inner
		if isEmpty(raw) {
			return s, nil
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(&s); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// unwrap returns the value of a top-level "bem" key, if that is the only
// key.
func unwrap(raw json.RawMessage) (json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil, false
	}
	inner, ok := m["bem"]
	if !ok || len(m) != 1 {
		return nil, false
	}
	return inner, true
}

// Merge deep-merges over into base: objects merge key by key, anything
// else in over replaces base. Either may be empty. A "bem" wrapper on
// either side is removed first.
func Merge(base, over json.RawMessage) (json.RawMessage, error) {
	decode := func(raw json.RawMessage) (any, error) {
		raw = bytes.TrimSpace(raw)
		if isEmpty(raw) {
			return nil, nil
		}
		if inner, ok := unwrap(raw); ok {
			raw = inner
			if isEmpty(raw) {
				return nil, nil
			}
		}
		var v any
		err := json.Unmarshal(raw, &v)
		return v, err
	}
	b, err := decode(base)
	if err != nil {
		return nil, err
	}
	o, err := decode(over)
	if err != nil {
		return nil, err
	}
	return json.Marshal(mergeValues(b, o))
}

// isEmpty reports whether raw means "no settings". Lua clients such as
// Neovim encode an empty table as [], so that counts too.
func isEmpty(raw []byte) bool {
	s := string(bytes.TrimSpace(raw))
	return s == "" || s == "null" || s == "[]" || s == "{}"
}

func mergeValues(base, over any) any {
	if over == nil {
		return base
	}
	bm, bok := base.(map[string]any)
	om, ook := over.(map[string]any)
	if !bok || !ook {
		return over
	}
	out := make(map[string]any, len(bm)+len(om))
	for k, v := range bm {
		out[k] = v
	}
	for k, v := range om {
		out[k] = mergeValues(bm[k], v)
	}
	return out
}

// FileError is a problem in bem.config.json, with the byte offset of a
// syntax error when known (-1 otherwise).
type FileError struct {
	Path   string
	Offset int
	Err    error
}

func (e *FileError) Error() string { return fmt.Sprintf("%s: %v", e.Path, e.Err) }

func (e *FileError) Unwrap() error { return e.Err }

// LoadFile reads and checks a configuration file. It returns the raw JSON
// for merging; the file is also decoded strictly to report mistakes.
func LoadFile(path string) (json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")) // UTF-8 BOM
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	if _, err := Parse(b, true); err != nil {
		off := -1
		var se *json.SyntaxError
		var te *json.UnmarshalTypeError
		switch {
		case errors.As(err, &se):
			off = max(int(se.Offset)-1, 0) // Offset is just past the bad byte
		case errors.As(err, &te):
			off = int(te.Offset)
		}
		return nil, &FileError{Path: path, Offset: off, Err: err}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil {
		return nil, &FileError{Path: path, Offset: -1, Err: errors.New("the configuration must be a JSON object")}
	}
	return b, nil
}

// Find looks for bem.config.json in dir and its parents, stopping after the
// first directory that contains .git (the project root). It returns "" if
// there is none.
func Find(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		p := filepath.Join(dir, FileName)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// NamingConvention compiles the naming convention.
func (s Settings) NamingConvention() (*bem.Naming, error) {
	return bem.NewNaming(s.Naming)
}

// GeneratorOptions returns the generator options for naming. Classes
// matching the ignore patterns are skipped.
func (s Settings) GeneratorOptions(naming *bem.Naming) (generator.Options, error) {
	opts := generator.Options{Naming: naming}
	if len(s.Ignore) > 0 {
		var res []*regexp.Regexp
		for _, p := range s.Ignore {
			re, err := regexp.Compile(p)
			if err != nil {
				return opts, fmt.Errorf("ignore: bad pattern %q: %v", p, err)
			}
			res = append(res, re)
		}
		opts.Skip = func(class string) bool {
			for _, re := range res {
				if re.MatchString(class) {
					return true
				}
			}
			return false
		}
	}
	switch ind := strings.ToLower(string(s.Generator.Indent)); ind {
	case "":
	case "tab":
		opts.Indent = "\t"
	default:
		n, err := strconv.Atoi(ind)
		if err != nil || n < 1 || n > 16 {
			return opts, fmt.Errorf("generator.indent: %q is not 1-16 or \"tab\"", ind)
		}
		opts.Indent = strings.Repeat(" ", n)
	}
	var err error
	if opts.Nesting, err = generator.ParseNesting(s.Generator.Nesting); err != nil {
		return opts, fmt.Errorf("generator.nesting: %v", err)
	}
	return opts, nil
}

// Linter builds the BEM linter.
func (s Settings) Linter() (*bem.Linter, error) {
	return bem.NewLinter(bem.Config{Naming: s.Naming, Rules: s.Rules, Ignore: s.Ignore})
}

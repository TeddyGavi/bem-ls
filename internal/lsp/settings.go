package lsp

import (
	"fmt"
	"time"

	"scss-bem-gen/internal/bem"
	"scss-bem-gen/internal/config"
	"scss-bem-gen/internal/core"
	"scss-bem-gen/internal/generator"
	"scss-bem-gen/internal/index"
)

// Settings is the server configuration; see package config.
type Settings = config.Settings

// compiled is everything derived from Settings.
type compiled struct {
	settings  Settings
	linter    *bem.Linter
	indexCfg  index.Config
	genOpts   generator.Options
	delay     time.Duration
	ghostText bool
}

func compile(s Settings) (*compiled, error) {
	linter, err := s.Linter()
	if err != nil {
		return nil, err
	}
	c := &compiled{settings: s, linter: linter, delay: 200 * time.Millisecond, ghostText: true}
	if s.Misc.DiagnosticsDelay > 0 {
		c.delay = time.Duration(s.Misc.DiagnosticsDelay) * time.Millisecond
	}
	if s.GhostText.Enabled != nil {
		c.ghostText = *s.GhostText.Enabled
	}
	if c.genOpts, err = s.GeneratorOptions(linter.Naming()); err != nil {
		return nil, err
	}

	st := s.Stylesheet
	c.indexCfg = index.Config{
		Lookup:         st.Lookup,
		Extensions:     st.Extensions,
		MaxSearchDepth: st.MaxSearchDepth,
		LoadPaths:      st.LoadPaths,
		Aliases:        st.Aliases,
		Exclude:        st.Exclude,
		GlobalFallback: st.GlobalFallback,
		ConfigureParser: func(p *core.Parser) {
			if len(s.JSX.ClassAttributes) > 0 {
				p.ClassAttrs = s.JSX.ClassAttributes
			}
			if len(s.JSX.ClassFunctions) > 0 {
				p.ClassFuncs = s.JSX.ClassFunctions
			}
			if len(s.CSSModules.Identifiers) > 0 {
				p.ModuleIdents = s.CSSModules.Identifiers
			}
			if len(s.Styled.Identifiers) > 0 {
				p.StyledIdents = s.Styled.Identifiers
			}
		},
	}
	if err := c.indexCfg.Validate(); err != nil {
		return nil, fmt.Errorf("stylesheet: %v", err)
	}
	return c, nil
}

// isModuleIdent reports whether id is a CSS module in f.
func (c *compiled) isModuleIdent(f *core.File, id string) bool {
	if _, ok := f.ModuleImport(id); ok {
		return true
	}
	ids := c.settings.CSSModules.Identifiers
	if len(ids) == 0 {
		ids = []string{"styles", "classes"}
	}
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

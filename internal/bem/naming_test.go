package bem

import (
	"fmt"
	"strings"
	"testing"
)

// show renders a Name as "block|e1,e2|mod=val" plus the error, if any.
func show(n Name) string {
	s := n.Block + "|" + strings.Join(n.Elements, ",")
	if n.HasMod {
		s += "|" + n.Mod
		if n.HasVal {
			s += "=" + n.Val
		}
	}
	if n.Err != nil {
		s += " ERR " + n.Err.Error()
	}
	return s
}

func TestParsePresets(t *testing.T) {
	tests := []struct {
		preset, class, want, base string
	}{
		{PresetClassic, "card", "card|", "card"},
		{PresetClassic, "card__title", "card|title", "card__title"},
		{PresetClassic, "card__title--big", "card|title|big", "card__title"},
		{PresetClassic, "plan-summary__old-plan__row1", "plan-summary|old-plan,row1", ""},
		{PresetClassic, "card--big-one", "card||big-one", "card"},
		{PresetClassic, "card__semi_annulus", `card|semi_annulus ERR element "semi_annulus" does not match lower-case kebab-case (e.g. my-name)`, ""},
		{PresetClassic, "Card", `Card| ERR block "Card" does not match lower-case kebab-case (e.g. my-name)`, ""},
		{PresetClassic, "card--a--b", "card||a ERR more than one modifier", "card"},
		{PresetClassic, "card--a__b", `card||a ERR element "b" comes after the modifier`, "card"},
		{PresetClassic, "card____x", "card|,x ERR empty element name", ""},
		{PresetClassic, "__x", "|x ERR empty block name", ""},

		{PresetKV, "btn--size_lg", "btn||size=lg", "btn"},
		{PresetKV, "btn__icon--disabled", "btn|icon|disabled", "btn__icon"},
		{PresetKV, "btn--size_lg_x", "btn||size=lg ERR more than one modifier value", "btn"},
		{PresetKV, "btn_x", `btn| ERR unexpected "_"`, ""},

		{PresetReact, "Card", "Card|", "Card"},
		{PresetReact, "Card-title", "Card|title", "Card-title"},
		{PresetReact, "Card-Title_size_large", "Card|Title|size=large", "Card-Title"},
		{PresetReact, "Card_disabled", "Card||disabled", "Card"},
		{PresetReact, "card-title", `card|title ERR block "card" does not match PascalCase (e.g. MyName)`, ""},
		{PresetReact, "Card-title_Big", `Card|title|Big ERR modifier "Big" does not match camelCase (e.g. myName)`, ""},
	}
	for _, tt := range tests {
		n, err := NewNaming(NamingConfig{Preset: tt.preset})
		if err != nil {
			t.Fatal(err)
		}
		got := n.Parse(tt.class)
		if show(got) != tt.want {
			t.Errorf("%s Parse(%q) = %s\n want %s", tt.preset, tt.class, show(got), tt.want)
		}
		if tt.base != "" && got.Base() != tt.base {
			t.Errorf("%s Parse(%q).Base() = %q, want %q", tt.preset, tt.class, got.Base(), tt.base)
		}
		if got.Base()+got.ModSuffix() != tt.class {
			t.Errorf("%s %q: Base+ModSuffix = %q", tt.preset, tt.class, got.Base()+got.ModSuffix())
		}
	}
}

func TestCustomNaming(t *testing.T) {
	n, err := NewNaming(NamingConfig{
		Preset:      PresetCustom,
		ElementSep:  "-",
		ModifierSep: "_",
		ValueSep:    "_",
		Block:       `[a-z]+`,
		Element:     `[a-z]+`,
		Modifier:    `[a-z]+`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := show(n.Parse("menu-item_state_open")); got != "menu|item|state=open" {
		t.Errorf("got %s", got)
	}
	if got := n.Parse("menu-Item"); got.Valid() {
		t.Errorf("menu-Item should be invalid")
	}
	if s := n.Suggest(n.Parse("menu-Item")); s != "" {
		t.Errorf("custom naming suggested %q", s)
	}
	if got := n.Format("menu", []string{"item"}, "state", "open"); got != "menu-item_state_open" {
		t.Errorf("Format = %q", got)
	}
}

func TestPresetOverride(t *testing.T) {
	n, err := NewNaming(NamingConfig{Preset: PresetClassic, Element: `[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*`})
	if err != nil {
		t.Fatal(err)
	}
	if !n.Parse("card__semi_annulus").Valid() {
		t.Error("overridden element pattern not used")
	}
	if n.ElementSep() != "__" || n.ModifierSep() != "--" {
		t.Error("separators changed")
	}
}

func TestNewNamingErrors(t *testing.T) {
	for _, cfg := range []NamingConfig{
		{Preset: "bootstrap"},
		{Preset: PresetCustom, ElementSep: "-", ModifierSep: "-"},
		{Preset: PresetCustom, ElementSep: "_", ValueSep: "_"},
		{Preset: PresetClassic, Block: "("},
	} {
		if _, err := NewNaming(cfg); err == nil {
			t.Errorf("NewNaming(%+v) succeeded", cfg)
		}
	}
}

func TestSuggest(t *testing.T) {
	tests := []struct{ preset, class, want string }{
		{PresetClassic, "card__semi_annulus", "card__semi-annulus"},
		{PresetClassic, "myCard__titleText--isBig", "my-card__title-text--is-big"},
		{PresetClassic, "HTMLPage", "html-page"},
		{PresetClassic, "card--a__b", ""}, // structural: no guess
		{PresetClassic, "card", ""},       // already valid
		{PresetKV, "btn--Size_LG", "btn--size_lg"},
		{PresetReact, "card-title_is_big", "Card-title_is_big"},
		{PresetReact, "Card-item_IsOpen", "Card-item_isOpen"},
		{PresetReact, "Card-title_is-big", ""}, // "-" starts an element after the modifier
	}
	for _, tt := range tests {
		n, _ := NewNaming(NamingConfig{Preset: tt.preset})
		got := n.Suggest(n.Parse(tt.class))
		if got != tt.want {
			t.Errorf("%s Suggest(%q) = %q, want %q", tt.preset, tt.class, got, tt.want)
		}
	}
}

func TestSeverityText(t *testing.T) {
	for _, s := range []Severity{SeverityOff, SeverityHint, SeverityInfo, SeverityWarning, SeverityError} {
		b, _ := s.MarshalText()
		var back Severity
		if err := back.UnmarshalText(b); err != nil || back != s {
			t.Errorf("round trip of %v: %v, %v", s, back, err)
		}
	}
	if s, err := ParseSeverity("WARN"); err != nil || s != SeverityWarning {
		t.Errorf("ParseSeverity(WARN) = %v, %v", s, err)
	}
	if _, err := ParseSeverity("loud"); err == nil {
		t.Error("ParseSeverity(loud) succeeded")
	}
	if got := fmt.Sprint(SeverityHint); got != "hint" {
		t.Errorf("String() = %q", got)
	}
}

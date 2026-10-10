package generator

import (
	"strings"
	"testing"

	"scss-bem-gen/internal/bem"
	"scss-bem-gen/internal/core"
)

func gen(t *testing.T, src string, opts Options) string {
	t.Helper()
	root, err := core.ParseString("test", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return Generate(root, opts)
}

func TestGenerate(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		nesting Nesting
		want    string
	}{
		{
			name: "chained elements follow the DOM",
			src:  `<div className="a"><p className="a__b"><i className="a__b__c"/></p></div>`,
			want: ".a {\n  &__b {\n    &__c {}\n  }\n}\n",
		},
		{
			name: "flat element under a sibling element goes to its block",
			src:  `<div className="card"><div className="card__body"><h2 className="card__title"/></div></div>`,
			want: ".card {\n  &__body {}\n  &__title {}\n}\n",
		},
		{
			name: "multiple modifiers, modifiers first, duplicates merged",
			src: `<div className="b"><i className="b__e"/>` +
				`<i className="b__e b__e--x b__e--y"/><i className="b__e--x"/></div>` +
				`<div className="b b--z"/>`,
			want: ".b {\n  &--z {}\n  &__e {\n    &--x {}\n    &--y {}\n  }\n}\n",
		},
		{
			name: "unrelated block nests as descendant in DOM mode",
			src:  `<div className="page"><div className="card"/></div>`,
			want: ".page {\n  .card {}\n}\n",
		},
		{
			name:    "unrelated block is top-level in BEM mode",
			src:     `<div className="page"><div className="card"/></div>`,
			nesting: NestingBEM,
			want:    ".page {}\n\n.card {}\n",
		},
		{
			name:    "BEM mode nests under the longest used prefix",
			src:     `<i className="a__b__c"/><i className="a__b"/><i className="a__x__y"/>`,
			nesting: NestingBEM,
			want:    ".a {\n  &__b {\n    &__c {}\n  }\n  &__x__y {}\n}\n",
		},
		{
			name: "tag selectors only for class-free leaves",
			src:  `<div className="a"><div><img/><span className="a__s"/></div><p><b/></p></div>`,
			want: ".a {\n  img {}\n  &__s {}\n  p {\n    b {}\n  }\n}\n",
		},
		{
			name: "components, fragments, module and dynamic classes are skipped",
			src:  "<><Card className={styles.x}><Inner/><i className={`a--${v}`}/></Card></>",
			want: "",
		},
		{
			name: "invalid identifiers are skipped",
			src:  `<div className="ok w-1/2 hover:x 2col -x --y"/>`,
			want: ".ok {}\n\n.-x {}\n",
		},
		{
			name: "top-level element without its block",
			src:  `<i className="card__icon"/>`,
			want: ".card {\n  &__icon {}\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gen(t, tt.src, Options{Nesting: tt.nesting})
			if got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestGenerateIndent(t *testing.T) {
	got := gen(t, `<a className="a"><b className="a__b"/></a>`, Options{Indent: "\t"})
	if want := ".a {\n\t&__b {}\n}\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseNesting(t *testing.T) {
	for in, want := range map[string]Nesting{"": NestingDOM, "dom": NestingDOM, "BEM": NestingBEM} {
		if got, err := ParseNesting(in); err != nil || got != want {
			t.Errorf("ParseNesting(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseNesting("tree"); err == nil {
		t.Error("ParseNesting(tree) succeeded")
	}
}

func TestGenerateNamingPresets(t *testing.T) {
	tests := []struct {
		preset  string
		nesting Nesting
		src     string
		want    string
	}{
		{
			preset: bem.PresetReact,
			src: `<div className="Card Card_active"><h2 className="Card-title Card-title_size_lg"/>` +
				`<div className="Card-body"><p className="Card-text"/></div></div>`,
			want: ".Card {\n  &_active {}\n  &-title {\n    &_size_lg {}\n  }\n  &-body {}\n  &-text {}\n}\n",
		},
		{
			preset:  bem.PresetReact,
			nesting: NestingBEM,
			src:     `<p className="Card-text"/><div className="Badge"/>`,
			want:    ".Card {\n  &-text {}\n}\n\n.Badge {}\n",
		},
		{
			preset: bem.PresetKV,
			src:    `<button className="btn btn--size_lg btn--disabled"><i className="btn__icon btn__icon--pos_left"/></button>`,
			want:   ".btn {\n  &--size_lg {}\n  &--disabled {}\n  &__icon {\n    &--pos_left {}\n  }\n}\n",
		},
	}
	for _, tt := range tests {
		n, err := bem.NewNaming(bem.NamingConfig{Preset: tt.preset})
		if err != nil {
			t.Fatal(err)
		}
		got := gen(t, tt.src, Options{Naming: n, Nesting: tt.nesting})
		if got != tt.want {
			t.Errorf("%s/%v:\ngot:\n%s\nwant:\n%s", tt.preset, tt.nesting, got, tt.want)
		}
	}
}

func TestGenerateSkip(t *testing.T) {
	skip := func(c string) bool { return strings.HasPrefix(c, "js-") }
	got := gen(t, `<div className="a js-hook"><i className="js-only"/><b className="a__b"/></div>`, Options{Skip: skip})
	if want := ".a {\n  &__b {}\n}\n"; got != want { // the skipped <i> has a class, so it gets no tag selector
		t.Errorf("got %q, want %q", got, want)
	}
}

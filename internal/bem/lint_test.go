package bem

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TeddyGavi/bem-ls/internal/core"
)

var update = flag.Bool("update", false, "rewrite golden files")

func parse(t *testing.T, src string) *core.Node {
	t.Helper()
	root, err := core.ParseString("test", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return root
}

func lint(t *testing.T, cfg Config, in Input) []string {
	t.Helper()
	l, err := NewLinter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range l.Lint(in) {
		out = append(out, d.String())
	}
	return out
}

// only enables the given rules, at warning level.
func only(rules ...Rule) Config {
	cfg := Config{Rules: map[Rule]Severity{}}
	for _, r := range Rules {
		cfg.Rules[r] = SeverityOff
	}
	for _, r := range rules {
		cfg.Rules[r] = SeverityWarning
	}
	return cfg
}

func TestRules(t *testing.T) {
	tests := []struct {
		name string
		rule Rule
		src  string
		want []string
	}{
		{
			name: "invalid name with suggestion",
			rule: RuleInvalidName,
			src:  `<div className="card"><i className="card__bigIcon"/><i className="card--a--b"/></div>`,
			want: []string{
				`1:37 warning invalidName: "card__bigIcon" does not follow the classic naming convention: element "bigIcon" does not match lower-case kebab-case (e.g. my-name) (did you mean "card__big-icon"?)`,
				`1:67 warning invalidName: "card--a--b" does not follow the classic naming convention: more than one modifier`,
			},
		},
		{
			name: "nested element suggests the flat name",
			rule: RuleNestedElement,
			src:  `<div className="card"><i className="card__body__title--big"/></div>`,
			want: []string{
				`1:37 warning nestedElement: "card__body__title--big" is an element of an element; BEM elements belong directly to the block "card" (did you mean "card__title--big"?)`,
			},
		},
		{
			name: "no flat suggestion when it would collide",
			rule: RuleNestedElement,
			src: `<div className="b"><i className="b__x__a"/><i className="b__y__a"/>` +
				`<i className="b__x__c"/><i className="b__c"/></div>`,
			want: []string{
				`1:34 warning nestedElement: "b__x__a" is an element of an element; BEM elements belong directly to the block "b"`,
				`1:58 warning nestedElement: "b__y__a" is an element of an element; BEM elements belong directly to the block "b"`,
				`1:82 warning nestedElement: "b__x__c" is an element of an element; BEM elements belong directly to the block "b"`,
			},
		},
		{
			name: "modifier without base",
			rule: RuleModifierWithoutBase,
			src: `<div className="card card--big">` +
				`<i className={clsx('card__x', on && 'card__x--on')}/>` +
				`<i className="card__y--on"/></div>`,
			want: []string{
				`1:100 warning modifierWithoutBase: modifier "card__y--on" is used without "card__y" on the same element`,
			},
		},
		{
			name: "element outside block",
			rule: RuleElementOutsideBlock,
			src: `<div className="card"><p><i className="card__a"/></p></div>` +
				`<i className="card__b"/>` +
				`<div className="other"><i className="card__c"/></div>` +
				`<Card><i className="card__d"/></Card>` +
				`<div className="card__body"><i className="card__e"/></div>`,
			want: []string{
				`1:74 warning elementOutsideBlock: element "card__b" is not inside an element with block "card"`,
				`1:121 warning elementOutsideBlock: element "card__c" is not inside an element with block "card"`,
				`1:190 warning elementOutsideBlock: element "card__body" is not inside an element with block "card"`,
			},
		},
		{
			name: "multiple blocks mixed",
			rule: RuleMultipleBlocksMix,
			src:  `<div className="card card--x button page__body"/>`,
			want: []string{
				`1:30 warning multipleBlocksMix: element mixes blocks "card" and "button"`,
			},
		},
		{
			name: "dynamic and module classes are not BEM-checked",
			rule: RuleInvalidName,
			src:  "<div className={`Bad_${x} ${styles.camelCase}`}/>",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lint(t, only(tt.rule), Input{Root: parse(t, tt.src)})
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

func TestStylesheetRules(t *testing.T) {
	root := parse(t, `<div className={clsx(styles.root, styles['my-item'], styles.myItem, styles.gone, other.x)}>`+
		`<p className="card card__title card__title--big"/></div>`)
	got := lint(t, only(RuleMissingInStylesheet, RuleModuleClassMissing), Input{
		Root:       root,
		Stylesheet: NewNameSet("card", "card__title"),
		Modules:    map[string]ClassSet{"styles": NewNameSet("root", "my-item")},
	})
	want := []string{
		`1:76 warning moduleClassMissing: CSS module "styles" has no class "gone"`,
		`1:123 warning missingInStylesheet: no selector for ".card__title--big" in the stylesheet`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Without a stylesheet or module info those rules stay quiet.
	if got := lint(t, only(RuleMissingInStylesheet, RuleModuleClassMissing), Input{Root: root}); len(got) != 0 {
		t.Errorf("without stylesheet info: %q", got)
	}
}

func TestLintStylesheet(t *testing.T) {
	root := parse(t, "<div className={`card card--${size} ${styles.myItem}`}/>")
	used := CollectUsed(root)
	l, _ := NewLinter(Config{Ignore: []string{`^js-`}})
	pos := func(off int) core.Range {
		return core.Range{Start: core.Position{Offset: off, Line: 1, Col: off + 1}}
	}
	defs := []ClassDef{
		{"card", pos(0)},
		{"card--lg", pos(1)},
		{"my-item", pos(2)},
		{"myItem", pos(3)},
		{"card__unused", pos(4)},
		{"js-hook", pos(5)},
	}
	var got []string
	for _, d := range l.LintStylesheet(defs, used) {
		got = append(got, d.String())
	}
	want := []string{`1:5 hint unusedSelector: ".card__unused" is not used by any component`}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestConfig(t *testing.T) {
	root := parse(t, `<div className="Card js-Hook"><i className="Card__x"/></div>`)

	// Ignore patterns apply to every rule.
	got := lint(t, Config{Ignore: []string{`^js-`}, Rules: map[Rule]Severity{RuleInvalidName: SeverityError}}, Input{Root: root})
	if len(got) != 2 || !strings.Contains(got[0], "error invalidName") || strings.Contains(strings.Join(got, ""), "js-Hook") {
		t.Errorf("got %q", got)
	}

	// The react preset accepts what classic rejects.
	got = lint(t, Config{Naming: NamingConfig{Preset: PresetReact}, Ignore: []string{`^js-`}}, Input{Root: parse(t,
		`<div className="Card Card_active"><i className="Card-title"/></div>`)})
	if len(got) != 0 {
		t.Errorf("react: %q", got)
	}

	if _, err := NewLinter(Config{Rules: map[Rule]Severity{"noSuchRule": SeverityHint}}); err == nil {
		t.Error("unknown rule accepted")
	}
	if _, err := NewLinter(Config{Ignore: []string{"("}}); err == nil {
		t.Error("bad ignore pattern accepted")
	}
}

// TestLintGolden lints the core fixtures with the default configuration and
// compares with testdata/<fixture>.lint.golden.
func TestLintGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "core", "testdata", "*.[jt]sx"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	l, err := NewLinter(Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		root := parse(t, string(src))
		var b strings.Builder
		for _, d := range l.Lint(Input{Root: root}) {
			b.WriteString(d.String())
			b.WriteByte('\n')
		}
		golden := filepath.Join("testdata", strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))+".lint.golden")
		if *update {
			if err := os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(golden, []byte(b.String()), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%v (run go test -update to create it)", err)
		}
		if b.String() != string(want) {
			t.Errorf("%s:\n--- got\n%s--- want\n%s", golden, b.String(), want)
		}
	}
}

func TestModulesForEndToEnd(t *testing.T) {
	src := `import s from './Card.module.scss';
import * as other from './Other.module.css';
import './global.scss';
export const Card = () => <div className={clsx(s.root, s.missing, other.x, unknown.y)} />;`
	f, err := core.ParseFile("Card.tsx", src)
	if err != nil {
		t.Fatal(err)
	}
	sheets := map[string]ClassSet{"./Card.module.scss": NewNameSet("root")}
	var loaded []string
	mods := ModulesFor(f, func(imp core.Import) ClassSet {
		loaded = append(loaded, imp.Source)
		if set, ok := sheets[imp.Source]; ok {
			return set
		}
		return nil // ./Other.module.css can't be read: skipped
	})
	if strings.Join(loaded, ",") != "./Card.module.scss,./Other.module.css" {
		t.Errorf("loaded %q", loaded)
	}
	got := lint(t, only(RuleModuleClassMissing), Input{Root: f.Root, Modules: mods})
	want := []string{`4:58 warning moduleClassMissing: CSS module "s" has no class "missing"`}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStyledComponentIsNotABlockBoundary(t *testing.T) {
	src := "const Box = styled.div`x: y;`;\n" +
		`export const A = () => <Box><i className="card__a"/></Box>;` + "\n" +
		`export const B = () => <Unknown><i className="card__b"/></Unknown>;`
	f, err := core.ParseFile("x.tsx", src)
	if err != nil {
		t.Fatal(err)
	}
	got := lint(t, only(RuleElementOutsideBlock), Input{Root: f.Root})
	if len(got) != 1 || !strings.Contains(got[0], `"card__a"`) {
		t.Errorf("got %q", got)
	}
}

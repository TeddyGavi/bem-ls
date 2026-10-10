package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"scss-bem-gen/internal/bem"
	"scss-bem-gen/internal/scss"
)

// workspace is the test project, written to a temp dir.
var workspace = map[string]string{
	"src/styles/_tokens.scss": `/// Body text colour.
$text: #111;
$bp: 600px;
@mixin elevate($n) { box-shadow: 0 $n 0; }
@function rem($px) { @return $px / 16px * 1rem; }
`,
	"src/styles/_index.scss": `@forward "tokens";
@forward "list" as list-*;
`,
	"src/styles/_list.scss": `@mixin reset { margin: 0; }
$gap: 4px;
`,
	"src/styles/legacy.scss": `$legacy: red;
%legacy-box { border: 0; }
`,
	"src/styles/home.scss": `.home { &__hero {} }`,

	"src/components/Card/Card.tsx": `import './Card.scss';
import { Title } from './Card.styles';
import * as S from './Card.styles';
export const Card = () => (
  <div className="card">
    <Title className="card__title">Hi</Title>
    <S.Footer className="card__footer card__missing" />
  </div>
);
`,
	"src/components/Card/Card.scss": `@use '../../styles' as s;
@use '../../styles/tokens' as *;
@import '../../styles/legacy';
.card {
  color: s.$text;
  @include s.list-reset;
  @include elevate(2);
  margin: $bp rem(4px) s.$list-gap;
  border-color: $legacy;
  @extend %legacy-box;
  &__title { color: $text; }
  &__footer {}
  &__unused {}
}
`,
	"src/components/Card/Card.styles.ts": "import styled from 'styled-components';\n" +
		"export const Title = styled.h2`\n  font-size: $size;\n  .x { }\n`;\n" +
		"export const Footer = styled.footer`padding: 0;`;\n",

	"src/components/Button/Button.tsx":  `export const Button = () => <button className="btn"><i className="btn__icon"/></button>;`,
	"src/components/Button/Button.scss": `.btn { color: $legacy; &__icon {} }`,

	"src/components/Profile/index.tsx": `import s from './Profile.module.scss';
export default () => <div className={clsx(s.avatar, s.statsRow, s.missing)} />;
`,
	"src/components/Profile/Profile.module.scss": `.avatar {} .stats-row {}`,

	"src/components/Shared/Alert.tsx":   `export const Alert = () => <div className="alert" />;`,
	"src/components/Shared/Toast.tsx":   `export const Toast = () => <div className="toast" />;`,
	"src/components/Shared/Badge.tsx":   `export const Badge = () => <span className="badge" />;`,
	"src/components/Shared/Badge.scss":  `.badge {}`,
	"src/components/Shared/shared.scss": `.alert {} .toast {}`,

	"src/pages/Home.tsx": `export const Home = () => <main className="home"><h1 className="home__hero"/></main>;`,

	"src/legacy/Old.tsx":  `export const Old = () => <div className="old" />;`,
	"src/legacy/Old.sass": ".old\n  color: red\n",

	"node_modules/pkg/index.tsx": `export const X = () => <div className="x" />;`,
	".cache/junk.scss":           `.junk {}`,
}

func newWorkspace(t *testing.T) (*Index, string) {
	t.Helper()
	root := t.TempDir()
	for name, content := range workspace {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := New(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return ix, root
}

func rel(root string, paths ...string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		r, _ := filepath.Rel(root, p)
		out[i] = filepath.ToSlash(r)
	}
	return out
}

func TestScan(t *testing.T) {
	ix, root := newWorkspace(t)
	var got []string
	for _, d := range ix.Docs() {
		got = append(got, rel(root, d.Path)[0])
	}
	if len(got) != len(workspace)-2 {
		t.Errorf("loaded %d files, want %d: %q", len(got), len(workspace)-2, got)
	}
	for _, g := range got {
		if strings.HasPrefix(g, "node_modules") || strings.HasPrefix(g, ".cache") {
			t.Errorf("excluded file loaded: %s", g)
		}
	}
	old, _ := ix.Get(filepath.Join(root, "src/legacy/Old.sass"))
	if !old.Unsupported() {
		t.Error(".sass should be unsupported")
	}
	ts, _ := ix.Get(filepath.Join(root, "src/components/Card/Card.styles.ts"))
	if len(ts.StyledSheets) != 2 || len(ts.StyledSheets[0].Refs) != 1 {
		t.Errorf("styled sheets not parsed: %+v", ts.StyledSheets)
	}
}

func TestStylesheetsFor(t *testing.T) {
	ix, root := newWorkspace(t)
	tests := []struct{ jsx, want, primary string }{
		{"src/components/Card/Card.tsx", "src/components/Card/Card.scss(import)", "src/components/Card/Card.scss"},
		{"src/components/Button/Button.tsx", "src/components/Button/Button.scss(sibling)", "src/components/Button/Button.scss"},
		{"src/components/Profile/index.tsx", "src/components/Profile/Profile.module.scss(import)", ""},
		{"src/components/Shared/Alert.tsx", "src/components/Shared/shared.scss(directory)", "src/components/Shared/shared.scss"},
		{"src/components/Shared/Badge.tsx", "src/components/Shared/Badge.scss(sibling) src/components/Shared/shared.scss(directory)", "src/components/Shared/Badge.scss"},
		{"src/pages/Home.tsx", "src/styles/home.scss(parent)", "src/styles/home.scss"},
	}
	for _, tt := range tests {
		var got []string
		for _, l := range ix.StylesheetsFor(filepath.Join(root, tt.jsx)) {
			got = append(got, rel(root, l.Path)[0]+"("+l.Strategy+")")
		}
		if strings.Join(got, " ") != tt.want {
			t.Errorf("%s: %q, want %q", tt.jsx, strings.Join(got, " "), tt.want)
		}
		if p := strings.Join(rel(root, ix.PrimaryStylesheets(filepath.Join(root, tt.jsx))...), " "); p != tt.primary {
			t.Errorf("%s: primary %q, want %q", tt.jsx, p, tt.primary)
		}
	}
}

func TestResolveSass(t *testing.T) {
	ix, root := newWorkspace(t)
	from := filepath.Join(root, "src/components/Card/Card.scss")
	for url, want := range map[string]string{
		"../../styles":        "src/styles/_index.scss",
		"../../styles/tokens": "src/styles/_tokens.scss",
		"../../styles/legacy": "src/styles/legacy.scss",
		"../../styles/_list":  "src/styles/_list.scss",
		"sass:math":           "",
		"missing":             "",
	} {
		p, ok := ix.ResolveSass(from, url)
		got := ""
		if ok {
			got = rel(root, p)[0]
		}
		if got != want {
			t.Errorf("ResolveSass(%q) = %q, want %q", url, got, want)
		}
	}
}

// refsIn resolves every reference in a stylesheet and renders
// "name->file:line".
func refsIn(t *testing.T, ix *Index, root, path string) []string {
	t.Helper()
	d, err := ix.Get(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, sh := range d.Sheets() {
		for _, r := range sh.Refs {
			label := r.Kind.Sigil() + r.Name
			if r.Namespace != "" {
				label = r.Namespace + "." + label
			}
			var targets []string
			for _, def := range ix.Resolve(d.Path, sh, r) {
				targets = append(targets, fmt.Sprintf("%s:%d", filepath.Base(def.Path), def.Symbol.Range.Start.Line))
			}
			if len(targets) == 0 {
				targets = []string{"?"}
			}
			out = append(out, label+"->"+strings.Join(targets, ","))
		}
	}
	return out
}

func TestResolve(t *testing.T) {
	ix, root := newWorkspace(t)
	got := refsIn(t, ix, root, "src/components/Card/Card.scss")
	want := []string{
		"s.$text->_tokens.scss:2",    // @use as s → _index @forward tokens
		"s.list-reset->_list.scss:1", // @forward "list" as list-*
		"elevate->_tokens.scss:4",    // @use ... as *
		"$bp->_tokens.scss:3",        // @use ... as *
		"rem->_tokens.scss:5",        // function through as *
		"s.$list-gap->_list.scss:2",  // prefixed variable
		"$legacy->legacy.scss:1",     // @import
		"%legacy-box->legacy.scss:2", // placeholder through @import
		"$text->_tokens.scss:2",      // nested rule, @use as *
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("Card.scss refs:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Button.scss uses $legacy without importing it: the global fallback
	// finds it; turning the fallback off leaves it unresolved.
	if got := refsIn(t, ix, root, "src/components/Button/Button.scss"); strings.Join(got, " ") != "$legacy->legacy.scss:1" {
		t.Errorf("fallback: %q", got)
	}
	off := false
	ix2, _ := New(root, Config{GlobalFallback: &off})
	ix2.Scan(context.Background())
	if got := refsIn(t, ix2, root, "src/components/Button/Button.scss"); strings.Join(got, " ") != "$legacy->?" {
		t.Errorf("without fallback: %q", got)
	}

	// Variables in a styled template resolve through the fallback too.
	if got := refsIn(t, ix, root, "src/components/Card/Card.styles.ts"); strings.Join(got, " ") != "$size->?" {
		t.Errorf("styled template refs: %q", got)
	}
}

func TestReferences(t *testing.T) {
	ix, root := newWorkspace(t)
	tokens, _ := ix.Get(filepath.Join(root, "src/styles/_tokens.scss"))
	text := tokens.Sheet.Global(scss.SymVariable, "text")
	var got []string
	for _, l := range ix.References(Def{Path: tokens.Path, Symbol: text}) {
		got = append(got, fmt.Sprintf("%s:%d:%d", rel(root, l.Path)[0], l.Range.Start.Line, l.Range.Start.Col))
	}
	sort.Strings(got)
	want := "src/components/Card/Card.scss:11:21 src/components/Card/Card.scss:5:10"
	if strings.Join(got, " ") != want {
		t.Errorf("references of $text: %q, want %q", strings.Join(got, " "), want)
	}
}

func TestLintThroughIndex(t *testing.T) {
	ix, root := newWorkspace(t)
	l, _ := bem.NewLinter(bem.Config{})
	lint := func(path string) []string {
		in, err := ix.LintInput(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, d := range l.Lint(in) {
			out = append(out, string(d.Rule)+":"+d.Class)
		}
		return out
	}
	if got := lint("src/components/Card/Card.tsx"); strings.Join(got, " ") != "missingInStylesheet:card__missing" {
		t.Errorf("Card: %q", got)
	}
	if got := lint("src/components/Profile/index.tsx"); strings.Join(got, " ") != "moduleClassMissing:missing" {
		t.Errorf("Profile: %q", got)
	}
	// Old.tsx pairs with Old.sass, which can't be parsed yet, so
	// missingInStylesheet stays quiet instead of flagging every class.
	if p := rel(root, ix.PrimaryStylesheets(filepath.Join(root, "src/legacy/Old.tsx"))...); strings.Join(p, "") != "src/legacy/Old.sass" {
		t.Errorf("Old primary = %q", p)
	}
	if got := lint("src/legacy/Old.tsx"); len(got) != 0 {
		t.Errorf("Old: %q", got)
	}

	defs, used, ok := ix.StylesheetLintInput(filepath.Join(root, "src/components/Card/Card.scss"))
	if !ok {
		t.Fatal("Card.scss has no components")
	}
	var unused []string
	for _, d := range l.LintStylesheet(defs, used) {
		unused = append(unused, d.Class)
	}
	if strings.Join(unused, " ") != "card__unused" {
		t.Errorf("unused: %q", unused)
	}
	// The shared partial is used by Card through @use, so it is judged too.
	if _, _, ok := ix.StylesheetLintInput(filepath.Join(root, "src/styles/legacy.scss")); !ok {
		t.Error("legacy.scss should be linked to Card through @import")
	}
	if _, _, ok := ix.StylesheetLintInput(filepath.Join(root, "src/styles/_list.scss")); !ok {
		t.Error("_list.scss should be linked to Card through @use/@forward")
	}
}

func TestClassLinks(t *testing.T) {
	ix, root := newWorkspace(t)
	card := filepath.Join(root, "src/components/Card/Card.tsx")
	d, _ := ix.Get(card)
	title := d.JSX.Root.Children[0].Children[0].Classes[0]
	defs := ix.ClassDefinitions(card, title)
	if len(defs) != 1 || filepath.Base(defs[0].Path) != "Card.scss" || defs[0].Range.Start.Line != 11 {
		t.Errorf("definitions of card__title: %+v", defs)
	}

	profile := filepath.Join(root, "src/components/Profile/index.tsx")
	pd, _ := ix.Get(profile)
	stats := pd.JSX.Root.Children[0].Classes[1] // s.statsRow
	if defs := ix.ClassDefinitions(profile, stats); len(defs) != 1 || filepath.Base(defs[0].Path) != "Profile.module.scss" {
		t.Errorf("definitions of s.statsRow: %+v", defs)
	}

	usages := ix.ClassUsages(filepath.Join(root, "src/components/Profile/Profile.module.scss"), "stats-row")
	if len(usages) != 1 || usages[0].Path != profile {
		t.Errorf("usages of .stats-row: %+v", usages)
	}
	usages = ix.ClassUsages(filepath.Join(root, "src/components/Card/Card.scss"), "card__title")
	if len(usages) != 1 || usages[0].Path != card {
		t.Errorf("usages of .card__title: %+v", usages)
	}
}

func TestResolveStyled(t *testing.T) {
	ix, root := newWorkspace(t)
	card := filepath.Join(root, "src/components/Card/Card.tsx")
	for _, tag := range []string{"Title", "S.Footer"} {
		p, def := ix.ResolveStyled(card, tag)
		if def == nil || filepath.Base(p) != "Card.styles.ts" {
			t.Errorf("ResolveStyled(%s) = %s, %+v", tag, p, def)
		}
	}
	if _, def := ix.ResolveStyled(card, "Nope"); def != nil {
		t.Error("Nope resolved")
	}
}

func TestOverlaysAndCaching(t *testing.T) {
	ix, root := newWorkspace(t)
	path := filepath.Join(root, "src/components/Button/Button.scss")

	d1, _ := ix.Get(path)
	d2, _ := ix.Get(path)
	if d1 != d2 {
		t.Error("unchanged file was re-parsed")
	}

	ix.Open(path, 2, ".btn { &__label {} }")
	if d, _ := ix.Get(path); d.Version != 2 || !d.Sheet.HasClass("btn__label") {
		t.Errorf("overlay not used: %+v", d)
	}
	ix.Change(path, 1, ".stale {}") // older version: ignored
	if d, _ := ix.Get(path); d.Sheet.HasClass("stale") {
		t.Error("older version replaced newer")
	}
	ix.Close(path)
	if d, _ := ix.Get(path); d.Version != -1 || !d.Sheet.HasClass("btn__icon") {
		t.Errorf("disk version not restored: %+v", d)
	}

	// A change on disk is picked up by modification time.
	later := time.Now().Add(2 * time.Second)
	os.WriteFile(path, []byte(".btn { &__new {} }"), 0o644)
	os.Chtimes(path, later, later)
	if d, _ := ix.Get(path); !d.Sheet.HasClass("btn__new") {
		t.Error("disk change not seen")
	}

	// Deleting the sibling stylesheet re-pairs the component.
	btn := filepath.Join(root, "src/components/Button/Button.tsx")
	os.Remove(path)
	ix.Invalidate(path)
	if got := ix.PrimaryStylesheets(btn); len(got) != 0 {
		t.Errorf("deleted stylesheet still primary: %q", got)
	}
	if _, err := ix.Get(path); err == nil {
		t.Error("Get of deleted file succeeded")
	}
}

func TestConcurrentUse(t *testing.T) {
	ix, root := newWorkspace(t)
	path := filepath.Join(root, "src/components/Card/Card.scss")
	card := filepath.Join(root, "src/components/Card/Card.tsx")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for v := 0; v < 20; v++ {
				switch (i + v) % 4 {
				case 0:
					ix.Open(path, i*100+v, fmt.Sprintf(".card { &__v%d {} }", v))
				case 1:
					ix.LintInput(card)
				case 2:
					ix.Scan(context.Background())
				case 3:
					ix.ClassUsages(path, "card__title")
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestScanCancel(t *testing.T) {
	ix, _ := newWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ix.Scan(ctx); err == nil {
		t.Error("cancelled scan returned no error")
	}
}

func TestConfigValidate(t *testing.T) {
	for _, c := range []Config{{Lookup: []string{"nearby"}}, {Extensions: []string{"scss"}}, {MaxSearchDepth: -1}} {
		if _, err := New(t.TempDir(), c); err == nil {
			t.Errorf("config %+v accepted", c)
		}
	}
}

func TestDocSheetAt(t *testing.T) {
	ix, root := newWorkspace(t)
	d, _ := ix.Get(filepath.Join(root, "src/components/Card/Card.styles.ts"))
	off := strings.Index(d.Text, "$size")
	if sh := d.SheetAt(off); sh == nil || sh.RefAt(off) == nil {
		t.Errorf("SheetAt(%d) did not find the template's ref", off)
	}
	if sh := d.SheetAt(0); sh != nil {
		t.Error("SheetAt outside templates returned a sheet")
	}
}

func BenchmarkScan(b *testing.B) {
	root := b.TempDir()
	const n = 1000
	for i := 0; i < n; i++ {
		dir := filepath.Join(root, "src", fmt.Sprintf("C%d", i))
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("C%d.tsx", i)), []byte(fmt.Sprintf(
			"import './C%d.scss';\nexport const C = () => (<div className=\"c%d\"><p className=\"c%d__a c%d__a--x\">{x}</p></div>);\n", i, i, i, i)), 0o644)
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("C%d.scss", i)), []byte(fmt.Sprintf(
			"@use '../tokens' as t;\n.c%d { color: t.$c; &__a { &--x { margin: 0; } } }\n", i)), 0o644)
	}
	os.WriteFile(filepath.Join(root, "src", "_tokens.scss"), []byte("$c: red;\n"), 0o644)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ix, _ := New(root, Config{})
		stats, err := ix.Scan(context.Background())
		if err != nil || stats.Files != 2*n+1 {
			b.Fatalf("scan: %+v %v", stats, err)
		}
	}
}

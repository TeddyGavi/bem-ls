package scss

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TeddyGavi/bem-ls/internal/core"
)

var update = flag.Bool("update", false, "rewrite golden files")

func lexAll(t testing.TB, src string) []Item {
	t.Helper()
	_, ch := Lex(src)
	var out []Item
	deadline := time.After(5 * time.Second)
	for {
		select {
		case it, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, it)
		case <-deadline:
			t.Fatalf("lexer hung on %q", src)
		}
	}
}

func TestLexTokens(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		{`.a{color:red}`, `Punct:. Ident:a Punct:{ Ident:color Punct:: Ident:red Punct:} EOF:`},
		{`$x: 1.5em;`, `Variable:$x Punct:: Space:  Number:1.5em Punct:; EOF:`},
		{`@media`, `AtKeyword:@media EOF:`},
		{`%p`, `Placeholder:%p EOF:`},
		{`#fff #{$a}`, `Hash:#fff Space:  InterpOpen:#{ Variable:$a Punct:} EOF:`},
		{`--x: -1px`, `Ident:--x Punct:: Space:  Punct:- Number:1px EOF:`},
		{`-webkit-box 50% .5`, `Ident:-webkit-box Space:  Number:50% Space:  Number:.5 EOF:`},
		{`url(//a.b/c.png) url("x")`, `Ident:url Punct:( URL://a.b/c.png Punct:) Space:  Ident:url Punct:( String:x Punct:) EOF:`},
		{"a // c\nb /* d */", "Ident:a Space:  Comment:// c Space:\n Ident:b Space:  Comment:/* d */ EOF:"},
		{`'it\'s' "q"`, `String:it\'s Space:  String:q EOF:`},
		{`.col-1\/2`, `Punct:. Ident:col-1\/2 EOF:`},
		{`v.$x`, `Ident:v Punct:. Variable:$x EOF:`},
		{`1e3 2E-2px`, `Number:1e3 Space:  Number:2E-2px EOF:`},
	}
	for _, tt := range tests {
		var got []string
		for _, it := range lexAll(t, tt.src) {
			got = append(got, strings.TrimPrefix(it.Typ.String(), "item")+":"+it.Val)
		}
		if g := strings.Join(got, " "); g != tt.want {
			t.Errorf("%q:\n got %s\nwant %s", tt.src, g, tt.want)
		}
	}
}

func TestLexErrors(t *testing.T) {
	for src, want := range map[string]string{
		`/* x`:      "1:1: unterminated comment",
		`a: "x`:     "1:4: unterminated string",
		`url(a.png`: "1:5: unterminated url(",
	} {
		var got []string
		for _, it := range lexAll(t, src) {
			if it.Typ == itemError {
				got = append(got, fmt.Sprintf("%d:%d: %s", it.Line, it.Col, it.Val))
			}
		}
		if strings.Join(got, "|") != want {
			t.Errorf("%q: errors %q, want %q", src, got, want)
		}
	}
}

func classesOf(s *Sheet, src string) string {
	var out []string
	for _, c := range s.Classes {
		out = append(out, c.Name+"="+src[c.Range.Start.Offset-0:c.Range.End.Offset])
	}
	return strings.Join(out, " ")
}

func TestSelectorResolution(t *testing.T) {
	tests := []struct {
		src       string
		selectors string // of the innermost rule
		classes   string
	}{
		{`.a { &__b { } }`, `.a__b`, `a=.a a__b=&__b`},
		{`.a, .b { &-x { } }`, `.a-x .b-x`, `a=.a b=.b a-x=&-x b-x=&-x`},
		{`.a { .b & { } }`, `.b .a`, `a=.a b=.b`},
		{`.a { & + & { } }`, `.a + .a`, `a=.a`},
		{`.a { .b, .c { } }`, `.a .b .a .c`, `a=.a b=.b c=.c`},
		{`.a:hover { &__b { } }`, `.a:hover__b`, `a=.a`}, // not a class: parent doesn't end in one
		{`div { &__b { } }`, `div__b`, ``},
		{`.a { &.is-x { } }`, `.a.is-x`, `a=.a is-x=.is-x`},
		{`.a { @media (x) { &__m { } } }`, `.a__m`, `a=.a a__m=&__m`},
		{`.a { @at-root &__r { } }`, `&__r`, `a=.a`},
		{`.a { @at-root { .r { } } }`, `.r`, `a=.a r=.r`},
		{`.a { &--#{$m} { } }`, `.a--#{$m}`, `a=.a`},
		{`.a[data-x=".no"] { }`, `.a[data-x=".no"]`, `a=.a`},
		{`@mixin m { &__x { } .y { } }`, `.y`, `y=.y`},
	}
	for _, tt := range tests {
		s := Parse("t.scss", tt.src)
		if len(s.Errors) > 0 {
			t.Errorf("%q: %v", tt.src, s.Errors)
		}
		n := s.Root
		for len(n.Children) > 0 {
			n = n.Children[len(n.Children)-1]
		}
		if got := strings.Join(n.Selectors, " "); got != tt.selectors {
			t.Errorf("%q: selectors %q, want %q", tt.src, got, tt.selectors)
		}
		if got := classesOf(s, tt.src); got != tt.classes {
			t.Errorf("%q: classes %q, want %q", tt.src, got, tt.classes)
		}
	}
}

func TestSymbolsAndLookup(t *testing.T) {
	src := `$x: 1;
.a {
  $x: 2;
  b: $x;
  .c { $x: 3 !global; d: $x; }
}
e: $x;
@mixin m($x) { f: $x; }
@each $x in $list { g: $x; }`
	s := Parse("t.scss", src)
	var got []string
	for _, r := range s.Refs {
		if r.Kind != SymVariable || r.Name != "x" {
			continue
		}
		sym := s.Lookup(r)
		if sym == nil {
			got = append(got, "nil")
			continue
		}
		got = append(got, fmt.Sprintf("%d:%s", sym.Range.Start.Line, sym.Value))
	}
	// b: $x → line 3 ($x: 2). d: $x → .a's $x (the !global one is global and
	// declared after the local). e: $x → the last global before it, line 5.
	// f: $x → the parameter. g: $x → the loop variable.
	want := "3:2 3:2 5:3 8: 9:"
	if strings.Join(got, " ") != want {
		t.Errorf("lookups %q, want %q", strings.Join(got, " "), want)
	}
	if g := s.Global(SymVariable, "x"); g == nil || g.Value != "1" {
		t.Errorf("Global(x) = %+v", g)
	}
}

func TestParseAt(t *testing.T) {
	// CSS of a styled template that starts at line 3, column 30 (offset 100)
	// of a .tsx file.
	base := core.Position{Offset: 100, Line: 3, Col: 30}
	s := ParseAt("Button.tsx", "\n  color: $c;\n  &:hover { .x { } }\n", base)
	if len(s.Errors) > 0 {
		t.Fatal(s.Errors)
	}
	if len(s.Refs) != 1 || s.Refs[0].Range.Start != (core.Position{Offset: 110, Line: 4, Col: 10}) {
		t.Errorf("ref = %+v", s.Refs)
	}
	if len(s.Classes) != 1 || s.Classes[0].Range.Start != (core.Position{Offset: 126, Line: 5, Col: 13}) {
		t.Errorf("classes = %+v", s.Classes)
	}
	if got := s.Root.Children[0].Selectors; strings.Join(got, ",") != "&:hover" {
		t.Errorf("top-level & = %q", got)
	}
	if len(s.Root.Decls) != 1 || s.Root.Decls[0].Property != "color" {
		t.Errorf("top-level decls = %+v", s.Root.Decls)
	}

	// Same line as the base: columns are shifted.
	s = ParseAt("x.tsx", "a: $b;", core.Position{Offset: 7, Line: 1, Col: 8})
	if r := s.Refs[0].Range; r.Start.Col != 11 || r.End.Col != 13 {
		t.Errorf("same-line ref = %+v", r)
	}
}

func TestErrorRecovery(t *testing.T) {
	src, _ := os.ReadFile("testdata/broken.scss")
	s := Parse("broken.scss", string(src))
	var errs []string
	for _, e := range s.Errors {
		errs = append(errs, e.Error())
	}
	want := []string{
		"5:1: unexpected '}'",
		"6:15: unterminated string",
		"7:4: unclosed block", // ".d {" is read as part of a rule inside .c
		"6:4: unclosed block",
	}
	if strings.Join(errs, "\n") != strings.Join(want, "\n") {
		t.Errorf("errors:\n%s\nwant:\n%s", strings.Join(errs, "\n"), strings.Join(want, "\n"))
	}
	// "color: red .b { x: y; }" has no ';', so it is one rule; .c and .d
	// are still found.
	if !s.HasClass("c") || !s.HasClass("d") {
		t.Errorf("classes after errors: %q", classesOf(s, string(src)))
	}
}

func TestDefaultNamespace(t *testing.T) {
	for url, want := range map[string]string{
		"sass:math": "math", "./vars": "vars", "src/_corners.scss": "corners", "a/b/c.module": "c", "theme": "theme",
	} {
		if got := DefaultNamespace(url); got != want {
			t.Errorf("DefaultNamespace(%q) = %q, want %q", url, got, want)
		}
	}
}

// dumpSheet renders everything Parse found, for golden files.
func dumpSheet(s *Sheet, src string) string {
	var b strings.Builder
	text := func(r core.Range) string { return src[r.Start.Offset:r.End.Offset] }
	at := func(r core.Range) string { return fmt.Sprintf("%d:%d", r.Start.Line, r.Start.Col) }
	b.WriteString("# imports\n")
	for _, i := range s.Imports {
		fmt.Fprintf(&b, "%s %s %q ns=%q prefix=%q show=%q hide=%q\n", at(i.URLRange), i.Kind, i.URL, i.Namespace, i.Prefix, i.Show, i.Hide)
	}
	b.WriteString("# tree\n")
	var walk func(n *Node, depth int)
	walk = func(n *Node, depth int) {
		for _, c := range n.Children {
			kind := "rule"
			if c.Kind == KindAtRule {
				kind = "@" + c.AtName
			}
			fmt.Fprintf(&b, "%s%s %s %q -> %q {%d:%d..%d:%d}\n", strings.Repeat("  ", depth), at(c.Range), kind,
				c.Prelude, c.Selectors, c.Open.Line, c.Open.Col, c.Close.Line, c.Close.Col)
			for _, d := range c.Decls {
				fmt.Fprintf(&b, "%s  %s decl %q: %q\n", strings.Repeat("  ", depth), at(d.PropRange), d.Property, d.Value)
			}
			walk(c, depth+1)
		}
	}
	walk(s.Root, 0)
	b.WriteString("# classes\n")
	for _, c := range s.Classes {
		fmt.Fprintf(&b, "%s %s from %q\n", at(c.Range), c.Name, text(c.Range))
	}
	b.WriteString("# symbols\n")
	for _, y := range s.Symbols {
		scope := "global"
		if y.Scope != nil {
			scope = "in " + strings.TrimSpace(y.Scope.AtName+" "+y.Scope.Prelude)
		}
		fmt.Fprintf(&b, "%s %s %s %s value=%q params=%q default=%v param=%v doc=%q\n",
			at(y.Range), y.Kind, y.Label(), scope, y.Value, y.Params, y.Default, y.Param, y.Doc)
	}
	b.WriteString("# refs\n")
	for _, r := range s.Refs {
		fmt.Fprintf(&b, "%s %s %q", at(r.Range), r.Kind, text(r.Range))
		if sym := s.Lookup(r); sym != nil {
			fmt.Fprintf(&b, " -> %s", at(sym.Range))
		}
		b.WriteByte('\n')
	}
	b.WriteString("# errors\n")
	for _, e := range s.Errors {
		b.WriteString(e.Error() + "\n")
	}
	return b.String()
}

func TestGolden(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.*css")
	if len(files) == 0 {
		t.Fatal("no fixtures")
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		got := dumpSheet(Parse(f, string(src)), string(src))
		golden := f + ".golden"
		if *update {
			if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%v (run go test -update)", err)
		}
		if got != string(want) {
			t.Errorf("%s differs:\n--- got\n%s--- want\n%s", golden, got, want)
		}
	}
}

func FuzzParse(f *testing.F) {
	files, _ := filepath.Glob("testdata/*.*css")
	for _, file := range files {
		b, _ := os.ReadFile(file)
		f.Add(string(b))
	}
	for _, s := range []string{"", "{", "}", "#{", "url(", "@use", "@mixin", "@include x using (", "a{&__b{&--c{}}}", "/*", "'", "@at-root", "$a:", "@each $a in"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		done := make(chan *Sheet, 1)
		go func() { done <- Parse("fuzz.scss", src) }()
		var s *Sheet
		select {
		case s = <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("parser hung")
		}
		ok := func(r core.Range) bool {
			return 0 <= r.Start.Offset && r.Start.Offset <= r.End.Offset && r.End.Offset <= len(src)
		}
		for _, c := range s.Classes {
			if !ok(c.Range) || c.Rule == nil {
				t.Fatalf("class %q: bad range %+v", c.Name, c.Range)
			}
		}
		for _, y := range s.Symbols {
			if !ok(y.Range) {
				t.Fatalf("symbol %q: bad range %+v", y.Name, y.Range)
			}
		}
		for _, r := range s.Refs {
			if !ok(r.Range) {
				t.Fatalf("ref %q: bad range %+v", r.Name, r.Range)
			}
		}
		var walk func(n *Node)
		walk = func(n *Node) {
			for _, c := range n.Children {
				if c.Parent != n || !ok(c.Range) {
					t.Fatalf("node %q: bad parent or range %+v", c.Prelude, c.Range)
				}
				walk(c)
			}
		}
		walk(s.Root)
	})
}

func BenchmarkParse(b *testing.B) {
	src, _ := os.ReadFile("testdata/card.scss")
	b.SetBytes(int64(len(src)))
	for i := 0; i < b.N; i++ {
		Parse("card.scss", string(src))
	}
}

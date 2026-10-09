package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func lexAll(t testing.TB, src string) []Item {
	t.Helper()
	_, ch := Lex("test", src)
	var items []Item
	timeout := time.After(5 * time.Second)
	for {
		select {
		case it, ok := <-ch:
			if !ok {
				return items
			}
			items = append(items, it)
		case <-timeout:
			t.Fatalf("lexer did not finish within 5s; input %q", src)
		}
	}
}

// tok is a compact "type:value" form used in expectations.
func tok(it Item) string {
	return fmt.Sprintf("%s:%s", strings.TrimPrefix(it.Typ.String(), "item"), it.Val)
}

func toks(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = tok(it)
	}
	return out
}

func TestLexTokens(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "element with class and text",
			src:  `<div className="a b">hi</div>`,
			want: []string{
				"TagOpen:<", "TagName:div", "AttrName:className", "Equal:=", "RawString:a b",
				"TagClose:>", "Text:hi", "TagCloseOpen:</", "TagCloseName:div", "TagClose:>", "EOF:",
			},
		},
		{
			name: "self-closing and boolean attribute",
			src:  `<input disabled />`,
			want: []string{"TagOpen:<", "TagName:input", "AttrName:disabled", "TagSelfClose:/>", "EOF:"},
		},
		{
			name: "fragment",
			src:  `<><b/></>`,
			want: []string{
				"TagOpen:<", "TagName:", "TagClose:>", "TagOpen:<", "TagName:b", "TagSelfClose:/>",
				"TagCloseOpen:</", "TagCloseName:", "TagClose:>", "EOF:",
			},
		},
		{
			name: "spread attribute",
			src:  `<a {...props} b="c"/>`,
			want: []string{
				"TagOpen:<", "TagName:a", "OpenBrace:{", "Punct:...", "Ident:props", "CloseBrace:}",
				"AttrName:b", "Equal:=", "RawString:c", "TagSelfClose:/>", "EOF:",
			},
		},
		{
			name: "multi-line expression keeps all tokens",
			src:  "<a x={\n  foo\n  .bar\n}/>",
			want: []string{
				"TagOpen:<", "TagName:a", "AttrName:x", "Equal:=", "OpenBrace:{", "Ident:foo",
				"Dot:.", "Ident:bar", "CloseBrace:}", "TagSelfClose:/>", "EOF:",
			},
		},
		{
			name: "braces inside strings, templates and comments",
			src:  "<a x={'}' + `{${\"}\"}` /* } */}/>",
			want: []string{
				"TagOpen:<", "TagName:a", "AttrName:x", "Equal:=", "OpenBrace:{", "String:}",
				"Punct:+", "TemplateOpen:`", "TemplatePart:{", "TemplateExprOpen:${", "String:}",
				"TemplateExprClose:}", "TemplateClose:`", "CloseBrace:}", "TagSelfClose:/>", "EOF:",
			},
		},
		{
			name: "clsx call with object",
			src:  `<a className={clsx('b', {'b--x': on})}/>`,
			want: []string{
				"TagOpen:<", "TagName:a", "AttrName:className", "Equal:=", "OpenBrace:{",
				"Ident:clsx", "Punct:(", "String:b", "Punct:,", "OpenBrace:{", "String:b--x",
				"Punct::", "Ident:on", "CloseBrace:}", "Punct:)", "CloseBrace:}",
				"TagSelfClose:/>", "EOF:",
			},
		},
		{
			name: "css module member and bracket access",
			src:  `<a className={styles['x-y'] + styles.z}/>`,
			want: []string{
				"TagOpen:<", "TagName:a", "AttrName:className", "Equal:=", "OpenBrace:{",
				"Ident:styles", "BracketString:x-y", "Punct:+", "Ident:styles", "Dot:.", "Ident:z",
				"CloseBrace:}", "TagSelfClose:/>", "EOF:",
			},
		},
		{
			name: "comparison is not a tag",
			src:  `if (a < b) x = c <d;`,
			want: []string{
				"Ident:if", "Punct:(", "Ident:a", "Punct:<", "Ident:b", "Punct:)", "Ident:x",
				"Punct:=", "Ident:c", "Punct:<", "Ident:d", "Punct:;", "EOF:",
			},
		},
		{
			name: "generic call and generic arrow are not tags",
			src:  `useState<string>(''); const f = <T,>(x: T) => x;`,
			want: []string{
				"Ident:useState", "Punct:<", "Ident:string", "Punct:>", "Punct:(", "String:",
				"Punct:)", "Punct:;", "Ident:const", "Ident:f", "Punct:=", "Punct:<", "Ident:T",
				"Punct:,", "Punct:>", "Punct:(", "Ident:x", "Punct::", "Ident:T", "Punct:)",
				"Punct:=>", "Ident:x", "Punct:;", "EOF:",
			},
		},
		{
			name: "return JSX and JSX in ternary",
			src:  `return c ? <a/> : <b/>`,
			want: []string{
				"Ident:return", "Ident:c", "Punct:?", "TagOpen:<", "TagName:a", "TagSelfClose:/>",
				"Punct::", "TagOpen:<", "TagName:b", "TagSelfClose:/>", "EOF:",
			},
		},
		{
			name: "child expression with nested JSX",
			src:  `<p>{ok && <i>y</i>}</p>`,
			want: []string{
				"TagOpen:<", "TagName:p", "TagClose:>", "OpenBrace:{", "Ident:ok", "Punct:&&",
				"TagOpen:<", "TagName:i", "TagClose:>", "Text:y", "TagCloseOpen:</", "TagCloseName:i",
				"TagClose:>", "CloseBrace:}", "TagCloseOpen:</", "TagCloseName:p", "TagClose:>", "EOF:",
			},
		},
		{
			name: "child text with quotes and comparison",
			src:  `<p>Don't 1 < 2 // no</p>`,
			want: []string{
				"TagOpen:<", "TagName:p", "TagClose:>", "Text:Don't 1 < 2 // no",
				"TagCloseOpen:</", "TagCloseName:p", "TagClose:>", "EOF:",
			},
		},
		{
			name: "regex containing quote and angle bracket",
			src:  `x = /'<a/g.test(s)`,
			want: []string{
				"Ident:x", "Punct:=", "Regex:/'<a/g", "Dot:.", "Ident:test", "Punct:(", "Ident:s",
				"Punct:)", "EOF:",
			},
		},
		{
			name: "division is not a regex",
			src:  `a / b / c`,
			want: []string{"Ident:a", "Punct:/", "Ident:b", "Punct:/", "Ident:c", "EOF:"},
		},
		{
			name: "member tag names and attribute comments",
			src:  `<UI.Card /* c */ data-x:y="1"></UI.Card>`,
			want: []string{
				"TagOpen:<", "TagName:UI.Card", "AttrName:data-x:y", "Equal:=", "RawString:1",
				"TagClose:>", "TagCloseOpen:</", "TagCloseName:UI.Card", "TagClose:>", "EOF:",
			},
		},
		{
			name: "JSX element as attribute value",
			src:  `<A icon=<b/> c="d"/>`,
			want: []string{
				"TagOpen:<", "TagName:A", "AttrName:icon", "Equal:=", "TagOpen:<", "TagName:b",
				"TagSelfClose:/>", "AttrName:c", "Equal:=", "RawString:d", "TagSelfClose:/>", "EOF:",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toks(lexAll(t, tt.src))
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("tokens for %q:\n got: %q\nwant: %q", tt.src, got, tt.want)
			}
		})
	}
}

func TestLexErrors(t *testing.T) {
	tests := []struct {
		src  string
		want []string // error messages, in order
	}{
		{`<div className="a`, []string{"unterminated attribute value", "unterminated tag"}},
		{`<div className`, []string{"unterminated tag"}},
		{`<div x={a`, []string{"unterminated expression: missing '}'", "unterminated tag"}},
		{"x = `abc", []string{"unterminated template literal"}},
		{"x = 'abc\ny'", []string{"unterminated string literal", "unterminated string literal"}},
		{`/* never closed`, []string{"unterminated comment"}},
		{`<a # />`, []string{`unexpected '#' in tag`}},
	}
	for _, tt := range tests {
		var got []string
		items := lexAll(t, tt.src)
		for _, it := range items {
			if it.Typ == itemError {
				got = append(got, it.Val)
			}
		}
		if strings.Join(got, "|") != strings.Join(tt.want, "|") {
			t.Errorf("errors for %q:\n got: %q\nwant: %q", tt.src, got, tt.want)
		}
		if last := items[len(items)-1]; last.Typ != itemEOF {
			t.Errorf("%q: last item is %v, want EOF", tt.src, last)
		}
	}
}

func TestLexPositions(t *testing.T) {
	src := "const x = 1;\n<div\n  className=\"a b\">é<span/></div>"
	items := lexAll(t, src)
	want := map[string][2]int{
		"Ident:const":        {1, 1},
		"Number:1":           {1, 11},
		"Punct:;":            {1, 12},
		"TagName:div":        {2, 2},
		"AttrName:className": {3, 3},
		"RawString:a b":      {3, 14},
		"Text:é":             {3, 19}, // Col counts bytes
		"TagName:span":       {3, 22},
		"TagSelfClose:/>":    {3, 26},
		"TagCloseOpen:</":    {3, 28},
		"TagCloseName:div":   {3, 30},
	}
	seen := 0
	for _, it := range items {
		if it.Typ != itemError && it.Typ != itemEOF && src[it.Pos:it.Pos+len(it.Val)] != it.Val {
			t.Errorf("%v: Val is not input[Pos:Pos+len(Val)]", it)
		}
		if w, ok := want[tok(it)]; ok {
			seen++
			if it.Line != w[0] || it.Col != w[1] {
				t.Errorf("%s at %d:%d, want %d:%d", tok(it), it.Line, it.Col, w[0], w[1])
			}
		}
	}
	if seen != len(want) {
		t.Errorf("matched %d expected tokens, want %d", seen, len(want))
	}
}

func TestLexStopDoesNotLeak(t *testing.T) {
	src := strings.Repeat(`<div className="a"><span>x</span></div>`, 10000)
	l, ch := Lex("big", src)
	<-ch
	l.Stop()
	l.Stop() // idempotent
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("token channel not closed after Stop")
	}
}

func TestLexStopBeforeRead(t *testing.T) {
	l, ch := Lex("x", strings.Repeat("<a/>", 1000))
	l.Stop()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("token channel not closed after Stop")
		}
	}
}

// readFixtures returns the contents of every .jsx/.tsx file in testdata.
func readFixtures(t testing.TB) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "*.[jt]sx"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(f)] = string(b)
	}
	return out
}

func TestLexFixturesHaveNoErrors(t *testing.T) {
	for name, src := range readFixtures(t) {
		for _, it := range lexAll(t, src) {
			if it.Typ == itemError {
				t.Errorf("%s: %v", name, it)
			}
		}
	}
}

func TestLexAdjacentTopLevelElements(t *testing.T) {
	got := toks(lexAll(t, `<a></a><b/><c/>`))
	want := []string{
		"TagOpen:<", "TagName:a", "TagClose:>", "TagCloseOpen:</", "TagCloseName:a", "TagClose:>",
		"TagOpen:<", "TagName:b", "TagSelfClose:/>", "TagOpen:<", "TagName:c", "TagSelfClose:/>", "EOF:",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

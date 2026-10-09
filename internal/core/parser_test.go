package core

import (
	"fmt"
	"strings"
	"testing"
)

// dump renders a tree as indented "tag .class(kind)" lines.
func dump(n *Node) string {
	var b strings.Builder
	var walk func(n *Node, depth int)
	walk = func(n *Node, depth int) {
		if !n.IsRoot() {
			b.WriteString(strings.Repeat("  ", depth))
			tag := n.Tag
			if n.IsFragment() {
				tag = "<>"
			}
			b.WriteString(tag)
			for _, c := range n.Classes {
				fmt.Fprintf(&b, " .%s", c.Name)
				if c.Kind != ClassStatic {
					fmt.Fprintf(&b, "(%s)", c.Kind)
				}
			}
			b.WriteByte('\n')
			depth++
		}
		for _, c := range n.Children {
			walk(c, depth)
		}
	}
	walk(n, 0)
	return b.String()
}

func mustParse(t *testing.T, src string) *Node {
	t.Helper()
	root, err := ParseString("test", src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	return root
}

func TestParseTree(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{
			name: "closing tags pop",
			src:  `<div className="a"><p className="a__b"></p><p className="a__c"></p></div>`,
			want: "div .a\n  p .a__b\n  p .a__c\n",
		},
		{
			name: "self-closing tags pop",
			src:  `<ul><li className="x"/><li className="y" /></ul>`,
			want: "ul\n  li .x\n  li .y\n",
		},
		{
			name: "several top-level trees",
			src:  `const A = () => <a className="a"/>; const B = () => (<b className="b"><i/></b>);`,
			want: "a .a\nb .b\n  i\n",
		},
		{
			name: "fragments and children expressions",
			src:  `<><p>{ok ? <i className="y"/> : <b>{x}</b>}</p></>`,
			want: "<>\n  p\n    i .y\n    b\n",
		},
		{
			name: "JSX inside non-class attributes becomes children",
			src:  `<Btn icon={<Icon className="ic"/>} render={() => <span className="r">x</span>}>t</Btn>`,
			want: "Btn\n  Icon .ic\n  span .r\n",
		},
		{
			name: "class and className, extra spaces",
			src:  "<div class=\"  a\n b \" className='c'/>",
			want: "div .a .b .c\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dump(mustParse(t, tt.src)); got != tt.want {
				t.Errorf("tree:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestExtractClasses(t *testing.T) {
	tests := []struct {
		expr string
		want string
	}{
		{`"a b"`, "a b"},
		{`'a'`, "a"},
		{`cond ? 'a' : 'b'`, "a(conditional) b(conditional)"},
		{`'a ' + (on ? 'a--on' : '')`, "a a--on(conditional)"},
		{"`a ${on ? 'a--on' : ''}`", "a a--on(conditional)"},
		{"`btn btn--${size} x`", "btn btn--(dynamic) x"},
		{"`${a}b c`", "b(dynamic) c"},
		{`'btn--' + size`, "btn--(dynamic)"},
		{`clsx('a', on && 'a--on', {'a--x': x, y: z}, [w && 'q'])`,
			"a a--on(conditional) a--x(conditional) y(conditional) q(conditional)"},
		{`classNames('a', {[styles.k]: v})`, "a k(module)"},
		{`styles.root`, "root(module)"},
		{`styles['my-root']`, "my-root(module)"},
		{`clsx(styles.a, isB && styles['b'])`, "a(module) b(module)"},
		{`size === 'lg' ? 'big' : 'small'`, "big(conditional) small(conditional)"},
		{`t('label.key')`, ""},
		{`getClass('x', 'y')`, ""},
		{`['a', on && 'b'].join(' ')`, "a b(conditional)"},
		{`clsx({ a: x ? 1 : 2, b: y })`, "a(conditional) b(conditional)"},
		{`map['k']`, ""},
		{`props.className`, ""},
	}
	for _, tt := range tests {
		src := fmt.Sprintf("<a className={%s}/>", tt.expr)
		root := mustParse(t, src)
		var got []string
		for _, c := range root.Children[0].Classes {
			s := c.Name
			if c.Kind != ClassStatic {
				s += "(" + c.Kind.String() + ")"
			}
			got = append(got, s)
		}
		if g := strings.Join(got, " "); g != tt.want {
			t.Errorf("className={%s}\n got: %s\nwant: %s", tt.expr, g, tt.want)
		}
	}
}

func TestClassRanges(t *testing.T) {
	src := "<a className={clsx(\n  'x y',\n  styles.z)}/>"
	root := mustParse(t, src)
	cls := root.Children[0].Classes
	if len(cls) != 3 {
		t.Fatalf("got %d classes, want 3", len(cls))
	}
	want := []struct {
		name            string
		line, col, ecol int
	}{
		{"x", 2, 4, 5}, {"y", 2, 6, 7}, {"z", 3, 10, 11},
	}
	for i, w := range want {
		c := cls[i]
		if c.Name != w.name || c.Range.Start.Line != w.line || c.Range.Start.Col != w.col || c.Range.End.Col != w.ecol {
			t.Errorf("class %d = %s %+v, want %s at %d:%d-%d", i, c.Name, c.Range, w.name, w.line, w.col, w.ecol)
		}
		if got := src[c.Range.Start.Offset:c.Range.End.Offset]; got != c.Name {
			t.Errorf("class %d: source range holds %q, want %q", i, got, c.Name)
		}
	}
}

func TestNodeRange(t *testing.T) {
	src := `x; <div><br/></div>`
	root := mustParse(t, src)
	div := root.Children[0]
	if got := src[div.Range.Start.Offset:div.Range.End.Offset]; got != `<div><br/></div>` {
		t.Errorf("div range = %q", got)
	}
	br := div.Children[0]
	if got := src[br.Range.Start.Offset:br.Range.End.Offset]; got != `<br/>` || !br.SelfClosing {
		t.Errorf("br range = %q, selfClosing=%v", got, br.SelfClosing)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		src, tree, errs string
	}{
		{
			src:  `<div><p></div>`,
			tree: "div\n  p\n",
			errs: "1:6: <p> is not closed",
		},
		{
			src:  `<div></span></div>`,
			tree: "div\n",
			errs: "1:6: unexpected closing tag </span>",
		},
		{
			src:  "<div>\n  <p>",
			tree: "div\n  p\n",
			errs: "2:3: <p> is not closed\n1:1: <div> is not closed",
		},
		{
			src:  `<div className="a`,
			tree: "div .a\n",
			errs: "1:16: unterminated attribute value\n1:1: unterminated tag\n1:1: <div> is not closed",
		},
	}
	for _, tt := range tests {
		root, err := ParseString("t", tt.src)
		if got := dump(root); got != tt.tree {
			t.Errorf("%q tree:\n%s\nwant:\n%s", tt.src, got, tt.tree)
		}
		if err == nil {
			t.Errorf("%q: no error, want %q", tt.src, tt.errs)
			continue
		}
		if err.Error() != tt.errs {
			t.Errorf("%q errors:\n got: %s\nwant: %s", tt.src, err, tt.errs)
		}
	}
}

func TestParseFixturesHaveNoErrors(t *testing.T) {
	for name, src := range readFixtures(t) {
		if _, err := ParseString(name, src); err != nil {
			t.Errorf("%s:\n%v", name, err)
		}
	}
}

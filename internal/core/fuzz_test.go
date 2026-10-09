package core

import (
	"strings"
	"testing"
	"time"
)

var fuzzSeeds = []string{
	``,
	`<`,
	`</`,
	`<div`,
	`<div className`,
	`<div className=`,
	`<div className="a`,
	`<div x={`,
	`<div x={a}`,
	`<div {...p`,
	"<a x={`${`}/>",
	"`${",
	`'`,
	`/*`,
	`/[/`,
	`<>`,
	`</>`,
	`<a></b></a>`,
	`{}}}{{`,
	`<T,>() => <T/>`,
	`a < b > c`,
	`<a b=<c/>/>`,
	"<p>{x && <i>\n}</p>",
	`<a className={clsx('a', {b: c ? 'd' : 'e'}, styles['f'], styles.g)}/>`,
	"import s from './a.module.scss'; import * as t from 'b'; import {c as d} from 'e'",
	"import type",
	"import {",
	"const x = require('./y.css')",
	"const A = styled.div`a: ${b};`; <A/>",
	"const A = styled(B).attrs(() => ({}))<P>`${css`x`}`",
	"styled.div<<>`",
	"css`${`${`",
}

// Run the fuzzers with a short -fuzzminimizetime (e.g. 2s) in CI. The
// default of 60s per new interesting input makes progress look stalled.

// FuzzLex checks that the lexer always terminates, never panics, ends with
// itemEOF, and that every token's Val/Pos/Line/Col agree with the input.
func FuzzLex(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	for _, src := range readFixtures(f) {
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		_, ch := Lex("fuzz", src)
		var items []Item
		deadline := time.After(2 * time.Second)
	loop:
		for {
			select {
			case it, ok := <-ch:
				if !ok {
					break loop
				}
				items = append(items, it)
			case <-deadline:
				t.Fatalf("lexer hung after %d items", len(items))
			}
		}
		if len(items) == 0 || items[len(items)-1].Typ != itemEOF {
			t.Fatalf("token stream does not end with itemEOF")
		}
		lastPos := 0
		for _, it := range items {
			if it.Pos < 0 || it.Pos > len(src) {
				t.Fatalf("%v: Pos out of range", it)
			}
			line := strings.Count(src[:it.Pos], "\n") + 1
			col := it.Pos - (strings.LastIndexByte(src[:it.Pos], '\n') + 1) + 1
			if it.Line != line || it.Col != col {
				t.Fatalf("%v: position is %d:%d, want %d:%d", it, it.Line, it.Col, line, col)
			}
			if it.Typ == itemError {
				continue
			}
			if it.Pos+len(it.Val) > len(src) || src[it.Pos:it.Pos+len(it.Val)] != it.Val {
				t.Fatalf("%v: Val is not input[Pos:Pos+len(Val)]", it)
			}
			if it.Pos < lastPos {
				t.Fatalf("%v: token starts before the previous one (%d)", it, lastPos)
			}
			lastPos = it.Pos
		}
	})
}

// FuzzParse checks that the parser never panics and that every class range
// points at its name in the source.
func FuzzParse(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		done := make(chan *File, 1)
		go func() {
			f, _ := ParseFile("fuzz", src)
			done <- f
		}()
		var f *File
		select {
		case f = <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("parser hung")
		}
		root := f.Root
		within := func(inner, outer Range) bool {
			return outer.Start.Offset <= inner.Start.Offset && inner.Start.Offset <= inner.End.Offset &&
				inner.End.Offset <= outer.End.Offset
		}
		for _, d := range f.Styled {
			if !within(d.Template, d.Range) || d.Range.End.Offset > len(src) {
				t.Fatalf("styled %q: template %+v outside range %+v", d.Name, d.Template, d.Range)
			}
			for _, h := range d.Holes {
				if !within(h, d.Template) {
					t.Fatalf("styled %q: hole %+v outside template %+v", d.Name, h, d.Template)
				}
			}
			if css := f.TemplateCSS(d); len(css) != d.Template.End.Offset-d.Template.Start.Offset {
				t.Fatalf("styled %q: TemplateCSS has length %d", d.Name, len(css))
			}
		}
		for _, imp := range f.Imports {
			r := imp.SourceRange
			if r.Start.Offset < 0 || r.End.Offset > len(src) || src[r.Start.Offset:r.End.Offset] != imp.Source {
				t.Fatalf("import %q: bad SourceRange %+v", imp.Source, r)
			}
		}
		var check func(n *Node)
		check = func(n *Node) {
			for _, c := range n.Classes {
				s, e := c.Range.Start.Offset, c.Range.End.Offset
				if s < 0 || e > len(src) || s > e || src[s:e] != c.Name {
					t.Fatalf("class %q has range [%d,%d)", c.Name, s, e)
				}
			}
			for _, k := range n.Children {
				if k.Parent != n {
					t.Fatal("child has wrong parent")
				}
				check(k)
			}
		}
		check(root)
	})
}

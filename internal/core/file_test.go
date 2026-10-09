package core

import (
	"fmt"
	"strings"
	"testing"
)

func mustParseFile(t *testing.T, src string) *File {
	t.Helper()
	f, err := ParseFile("test.tsx", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return f
}

func showImport(i Import) string {
	var parts []string
	if i.TypeOnly {
		parts = append(parts, "type")
	}
	if i.Require {
		parts = append(parts, "require")
	}
	if i.Default != "" {
		parts = append(parts, "default="+i.Default)
	}
	if i.Namespace != "" {
		parts = append(parts, "ns="+i.Namespace)
	}
	for _, n := range i.Named {
		if n.Name == n.Local {
			parts = append(parts, "{"+n.Name+"}")
		} else {
			parts = append(parts, "{"+n.Name+" as "+n.Local+"}")
		}
	}
	parts = append(parts, "from="+i.Source)
	if i.IsModule() {
		parts = append(parts, "module")
	} else if i.IsStylesheet() {
		parts = append(parts, "stylesheet")
	}
	return strings.Join(parts, " ")
}

func TestImports(t *testing.T) {
	src := `
import React, { useState as useS, type FC } from 'react';
import type { Props } from './types';
import styles from './Card.module.scss';
import * as s from "./other.module.css";
import './global.scss';
import plain from './theme.scss?inline';
import { a, "b-c" as bc } from './named';
const legacy = require('./Legacy.module.sass');
require('./side.css');
const lazy = import('./Lazy');
const url = import.meta.url;
foo.require('./not-this.css');
`
	f := mustParseFile(t, src)
	var got []string
	for _, i := range f.Imports {
		got = append(got, showImport(i))
	}
	want := []string{
		"default=React {useState as useS} {FC} from=react",
		"type {Props} from=./types",
		"default=styles from=./Card.module.scss module",
		"ns=s from=./other.module.css module",
		"from=./global.scss stylesheet",
		"default=plain from=./theme.scss?inline module",
		"{a} {b-c as bc} from=./named",
		"require default=legacy from=./Legacy.module.sass module",
		"require from=./side.css stylesheet",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("imports:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	imp, ok := f.ModuleImport("styles")
	if !ok || imp.Source != "./Card.module.scss" {
		t.Errorf("ModuleImport(styles) = %+v, %v", imp, ok)
	}
	if r := imp.SourceRange; src[r.Start.Offset:r.End.Offset] != "./Card.module.scss" {
		t.Errorf("SourceRange covers %q", src[r.Start.Offset:r.End.Offset])
	}
	if _, ok := f.ModuleImport("React"); ok {
		t.Error("React is not a module import")
	}
	if n := len(f.Stylesheets()); n != 6 {
		t.Errorf("Stylesheets() has %d entries, want 6", n)
	}
}

func TestImportedModuleIdentifiers(t *testing.T) {
	src := `
import css from './Card.module.scss';
const m = require('./Other.module.css');
export const Card = () => <div className={clsx(css.root, m['x-y'], other.z)} />;
`
	f := mustParseFile(t, src)
	var got []string
	for _, c := range f.Root.Children[0].Classes {
		got = append(got, fmt.Sprintf("%s.%s(%s)", c.Module, c.Name, c.Kind))
	}
	if want := "css.root(module) m.x-y(module)"; strings.Join(got, " ") != want {
		t.Errorf("classes = %q, want %q", strings.Join(got, " "), want)
	}
}

func showStyled(f *File, d *StyledDef) string {
	s := d.Kind.String()
	if d.Name != "" {
		s += " " + d.Name
	}
	if d.Tag != "" {
		s += " tag=" + d.Tag
	}
	if d.Extends != "" {
		s += " extends=" + d.Extends
	}
	return s + fmt.Sprintf(" css=%q", f.TemplateCSS(d))
}

func TestStyled(t *testing.T) {
	src := "import styled, { css, keyframes, createGlobalStyle } from 'styled-components';\n" +
		"const Button = styled.button`\n  color: ${p => p.color};\n  &:hover { color: red; }\n`;\n" +
		"export const Primary = styled(Button)`background: blue;`;\n" +
		"const Link = styled(ui.Link).attrs({ role: 'link' })`text-decoration: none;`;\n" +
		"const Box = styled.div<{ big: boolean }>`width: ${({ big }) => (big ? 2 : 1)}px;`;\n" +
		"const Deep = styled.div<Partial<Record<string, number>>>`a: b;`;\n" +
		"const Span = styled('span').withConfig({ shouldForwardProp: () => true })`c: d;`;\n" +
		"const fade = keyframes`from { opacity: 0; }`;\n" +
		"const mixin = css`margin: 0; ${p => p.on && css`outline: 1px;`}`;\n" +
		"const Global = createGlobalStyle`body { margin: 0; }`;\n" +
		"const notCss = theme.css`x`;\n" +
		"const label = `plain ${template}`;\n" +
		"const tagged = sql`select 1`;\n"
	f := mustParseFile(t, src)
	var got []string
	for _, d := range f.Styled {
		got = append(got, showStyled(f, d))
	}
	holes := func(s string) string { return strings.Repeat("_", len(s)) }
	want := []string{
		`styled Button tag=button css="\n  color: ` + holes("${p => p.color}") + `;\n  &:hover { color: red; }\n"`,
		`styled Primary extends=Button css="background: blue;"`,
		`styled Link extends=ui.Link css="text-decoration: none;"`,
		`styled Box tag=div css="width: ` + holes("${({ big }) => (big ? 2 : 1)}") + `px;"`,
		`styled Deep tag=div css="a: b;"`,
		`styled Span tag=span css="c: d;"`,
		`keyframes fade css="from { opacity: 0; }"`,
		`css css="outline: 1px;"`, // nested inside mixin's hole; closes first
		"css mixin css=\"margin: 0; " + holes("${p => p.on && css`outline: 1px;`}") + "\"",
		`global Global css="body { margin: 0; }"`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("styled:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Ranges point at the right source text.
	b := f.StyledByName("Button")
	if r := b.NameRange; src[r.Start.Offset:r.End.Offset] != "Button" {
		t.Errorf("NameRange covers %q", src[r.Start.Offset:r.End.Offset])
	}
	if r := b.Range; !strings.HasPrefix(src[r.Start.Offset:r.End.Offset], "styled.button`") ||
		!strings.HasSuffix(src[r.Start.Offset:r.End.Offset], "`") {
		t.Errorf("Range covers %q", src[r.Start.Offset:r.End.Offset])
	}
	if len(b.Holes) != 1 || src[b.Holes[0].Start.Offset:b.Holes[0].End.Offset] != "${p => p.color}" {
		t.Errorf("holes = %+v", b.Holes)
	}
	if b.Template.Start.Line != 2 || b.Template.Start.Col != 30 {
		t.Errorf("template starts at %d:%d, want 2:30", b.Template.Start.Line, b.Template.Start.Col)
	}
}

func TestStyledUsages(t *testing.T) {
	src := `
export function Card() {
  return (
    <Wrapper className="card">
      <Title>Hi</Title>
      <Title as="h3" />
      <Other />
    </Wrapper>
  );
}
const Wrapper = styled.section` + "`padding: 1rem;`" + `;
const Title = styled.h2` + "`margin: 0;`" + `;
`
	f := mustParseFile(t, src)
	wrapper := f.Root.Children[0]
	if wrapper.Styled == nil || wrapper.Styled.Name != "Wrapper" {
		t.Fatalf("Wrapper not linked: %+v", wrapper.Styled)
	}
	title := f.StyledByName("Title")
	if len(title.Usages) != 2 || title.Usages[0].Styled != title {
		t.Errorf("Title usages = %d", len(title.Usages))
	}
	if other := wrapper.Children[2]; other.Styled != nil {
		t.Errorf("Other linked to %v", other.Styled.Name)
	}
	if r := wrapper.TagRange; src[r.Start.Offset:r.End.Offset] != "Wrapper" {
		t.Errorf("TagRange covers %q", src[r.Start.Offset:r.End.Offset])
	}
	// The className on a styled component is still a real class.
	if len(wrapper.Classes) != 1 || wrapper.Classes[0].Name != "card" {
		t.Errorf("classes = %+v", wrapper.Classes)
	}
}

func TestStyledLongAttrsDoesNotPanic(t *testing.T) {
	// More tokens between styled and the template than are kept for
	// looking back: no match, but no crash either.
	src := "const A = styled.div.attrs({" + strings.Repeat("a: 1, ", 100) + "})`x: y;`;"
	f := mustParseFile(t, src)
	if len(f.Styled) > 1 {
		t.Errorf("got %d definitions", len(f.Styled))
	}
}

package lsp

import (
	"runtime"
	"testing"

	"scss-bem-gen/internal/bem"
	"scss-bem-gen/internal/lsp/protocol"
)

func TestAddSelectors(t *testing.T) {
	classic := bem.Classic()
	tests := []struct {
		name, text string
		classes    []string
		want       string
	}{
		{
			name:    "element into block, modifier into element",
			text:    ".card {\n  color: red;\n\n  &__title {\n    x: y;\n  }\n}\n",
			classes: []string{"card__body", "card__title--big"},
			want:    ".card {\n  color: red;\n\n  &__title {\n    x: y;\n    &--big {}\n  }\n  &__body {}\n}\n",
		},
		{
			name:    "missing parents are created",
			text:    ".card {\n}\n",
			classes: []string{"card__body--wide"},
			want:    ".card {\n  &__body {\n    &--wide {}\n  }\n}\n",
		},
		{
			name:    "new block appended; tabs kept",
			text:    ".a {\n\tb: c;\n}",
			classes: []string{"other", "other__x"},
			want:    ".a {\n\tb: c;\n}\n\n.other {\n\t&__x {}\n}\n",
		},
		{
			name:    "one-line block",
			text:    ".a { b: c; }\n",
			classes: []string{"a__x"},
			want:    ".a { b: c; &__x {} }\n",
		},
		{
			name:    "nested element under longest existing prefix",
			text:    ".a {\n  &__b {\n  }\n}\n",
			classes: []string{"a__b__c"},
			want:    ".a {\n  &__b {\n    &__c {}\n  }\n}\n",
		},
		{
			name:    "rule with a selector list is not nested into",
			text:    ".a, .z {\n}\n",
			classes: []string{"a__x"},
			want:    ".a, .z {\n}\n\n.a__x {}\n",
		},
		{
			name:    "empty file and existing class",
			text:    "",
			classes: []string{"a", "a"},
			want:    ".a {}\n",
		},
	}
	for _, tt := range tests {
		if got := addSelectors(tt.text, tt.classes, classic); got != tt.want {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, got, tt.want)
		}
	}
}

func TestDiffEdit(t *testing.T) {
	old := "a é b"
	newText := "a é X b"
	m := newMapper(old, protocol.EncodingUTF16)
	e := diffEdit(m, old, newText)
	if e.NewText != "X " || e.Range.Start != (protocol.Position{Line: 0, Character: 4}) || e.Range.End != e.Range.Start {
		t.Errorf("edit = %+v", e)
	}
}

func TestMapper(t *testing.T) {
	text := "a😀b\ncé"
	m16 := newMapper(text, protocol.EncodingUTF16)
	m8 := newMapper(text, protocol.EncodingUTF8)
	off := len("a😀")
	if p := m16.position(off); p != (protocol.Position{Line: 0, Character: 3}) {
		t.Errorf("utf16 position = %+v", p)
	}
	if p := m8.position(off); p != (protocol.Position{Line: 0, Character: 5}) {
		t.Errorf("utf8 position = %+v", p)
	}
	if o := m16.offset(protocol.Position{Line: 1, Character: 2}); o != len(text) {
		t.Errorf("offset at end = %d", o)
	}
	if o := m16.offset(protocol.Position{Line: 9, Character: 0}); o != len(text) {
		t.Errorf("clamped offset = %d", o)
	}
	got := applyChanges(text, []protocol.TextDocumentContentChangeEvent{
		{Range: &protocol.Range{Start: protocol.Position{Line: 0, Character: 1}, End: protocol.Position{Line: 0, Character: 3}}, Text: "-"},
		{Range: &protocol.Range{Start: protocol.Position{Line: 1, Character: 0}, End: protocol.Position{Line: 1, Character: 0}}, Text: ">"},
	}, protocol.EncodingUTF16)
	if got != "a-b\n>cé" {
		t.Errorf("applyChanges = %q", got)
	}
}

func TestURIs(t *testing.T) {
	path, want := "/tmp/a b/c#d.scss", protocol.DocumentURI("file:///tmp/a%20b/c%23d.scss")
	if runtime.GOOS == "windows" {
		path, want = `C:\tmp\a b\c#d.scss`, "file:///C:/tmp/a%20b/c%23d.scss"
	}
	uri := pathToURI(path)
	if uri != want {
		t.Errorf("pathToURI = %s, want %s", uri, want)
	}
	if p, err := uriToPath(uri); err != nil || p != path {
		t.Errorf("uriToPath = %q, %v", p, err)
	}
	if _, err := uriToPath("untitled:Untitled-1"); err == nil {
		t.Error("untitled URI accepted")
	}
}

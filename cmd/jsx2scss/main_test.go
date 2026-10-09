package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const card = `<div className="card"><div className="card__body"><h2 className="card__title"/></div></div>`

func runCLI(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestStdinToStdout(t *testing.T) {
	for _, args := range [][]string{nil, {"-"}, {"-i", "-"}} {
		code, out, errOut := runCLI(t, card, args...)
		if code != 0 || errOut != "" {
			t.Fatalf("args %q: exit %d, stderr %q", args, code, errOut)
		}
		if want := ".card {\n  &__body {}\n  &__title {}\n}\n"; out != want {
			t.Errorf("args %q: stdout %q, want %q", args, out, want)
		}
	}
}

func TestFileInAndOut(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "Card.jsx")
	out := filepath.Join(dir, "Card.scss")
	if err := os.WriteFile(in, []byte(card), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-i", in, "-o", out, "-indent", "tab"},
		{"--input", in, "--output", out, "-indent", "tab"},
		{"-o", out, "-indent=tab", in},
	} {
		os.Remove(out)
		code, stdout, stderr := runCLI(t, "", args...)
		if code != 0 || stdout != "" || stderr != "" {
			t.Fatalf("args %q: exit %d, stdout %q, stderr %q", args, code, stdout, stderr)
		}
		got, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if want := ".card {\n\t&__body {}\n\t&__title {}\n}\n"; string(got) != want {
			t.Errorf("args %q: wrote %q, want %q", args, got, want)
		}
	}
}

func TestNestingFlag(t *testing.T) {
	src := `<div className="page"><div className="card"/></div>`
	_, dom, _ := runCLI(t, src)
	_, bem, _ := runCLI(t, src, "-nesting", "bem")
	if dom != ".page {\n  .card {}\n}\n" || bem != ".page {}\n\n.card {}\n" {
		t.Errorf("dom %q, bem %q", dom, bem)
	}
}

func TestNamingFlag(t *testing.T) {
	src := `<div className="Card"><h2 className="Card-title Card-title_big"/></div>`
	code, out, errOut := runCLI(t, src, "-naming", "react")
	if want := ".Card {\n  &-title {\n    &_big {}\n  }\n}\n"; code != 0 || out != want {
		t.Errorf("exit %d, stdout %q, stderr %q; want %q", code, out, errOut, want)
	}
}

func TestParseErrorsExitNonZero(t *testing.T) {
	src := "<div className=\"a\">\n  <p className=\"a__b\">"
	code, out, errOut := runCLI(t, src)
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if out != "" {
		t.Errorf("stdout %q, want nothing", out)
	}
	for _, want := range []string{"<stdin>:2:3: <p> is not closed", "<stdin>:1:1: <div> is not closed", "-force"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q does not contain %q", errOut, want)
		}
	}

	code, out, _ = runCLI(t, src, "-force")
	if code != 0 || !strings.Contains(out, "&__b {}") {
		t.Errorf("-force: exit %d, stdout %q", code, out)
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"-i", "/does/not/exist.jsx"}, 1, "jsx2scss: open /does/not/exist.jsx: no such file or directory"},
		{[]string{"-nesting", "tree"}, 2, `unknown nesting "tree"`},
		{[]string{"-indent", "x"}, 2, `invalid -indent "x"`},
		{[]string{"-naming", "bootstrap"}, 2, `unknown naming preset "bootstrap"`},
		{[]string{"-naming", "custom"}, 2, "needs a config file"},
		{[]string{"-bogus"}, 2, "flag provided but not defined: -bogus"},
		{[]string{"a.jsx", "b.jsx"}, 2, "too many arguments"},
		{[]string{"-i", "a.jsx", "b.jsx"}, 2, "both by -i and as an argument"},
	}
	for _, tt := range tests {
		code, out, errOut := runCLI(t, card, tt.args...)
		if code != tt.code || out != "" || !strings.Contains(errOut, tt.msg) {
			t.Errorf("args %q: exit %d, stdout %q, stderr %q; want exit %d and %q",
				tt.args, code, out, errOut, tt.code, tt.msg)
		}
		if strings.Contains(errOut, "%v") || strings.Contains(errOut, "%!") {
			t.Errorf("args %q: badly formatted stderr %q", tt.args, errOut)
		}
	}
}

func TestHelp(t *testing.T) {
	code, out, errOut := runCLI(t, "", "-h")
	if code != 0 || !strings.HasPrefix(out, "Usage: jsx2scss") || errOut != "" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

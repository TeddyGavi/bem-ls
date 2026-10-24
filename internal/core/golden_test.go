package core_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TeddyGavi/bem-ls/internal/core"
	"github.com/TeddyGavi/bem-ls/internal/generator"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestGolden runs every testdata/*.jsx and *.tsx through the parser and the
// generator and compares the result with X.scss.golden (DOM nesting) and
// X.bem.scss.golden (BEM nesting). Run with -update to rewrite them.
func TestGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.[jt]sx"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no fixtures")
	}
	modes := []struct {
		suffix  string
		nesting generator.Nesting
	}{
		{".scss.golden", generator.NestingDOM},
		{".bem.scss.golden", generator.NestingBEM},
	}
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		root, err := core.ParseString(file, string(src))
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		for _, m := range modes {
			golden := strings.TrimSuffix(file, filepath.Ext(file)) + m.suffix
			t.Run(filepath.Base(golden), func(t *testing.T) {
				got := generator.Generate(root, generator.Options{Nesting: m.nesting})
				if *update {
					if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
						t.Fatal(err)
					}
					return
				}
				want, err := os.ReadFile(golden)
				if err != nil {
					t.Fatalf("%v (run go test -update to create it)", err)
				}
				if got != string(want) {
					t.Errorf("output differs from %s:\n--- got\n%s--- want\n%s", golden, got, want)
				}
			})
		}
	}
}

package bem

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

func allNamings(tb testing.TB) []*Naming {
	tb.Helper()
	var namings []*Naming
	for _, p := range []string{PresetClassic, PresetKV, PresetReact, PresetCustom} {
		n, err := NewNaming(NamingConfig{Preset: p})
		if err != nil {
			tb.Fatal(err)
		}
		namings = append(namings, n)
	}
	return namings
}

// validPart reports whether p can be used as a part matched by re under n
// and still come back unchanged from Parse(Format(...)). That means p matches
// its pattern, contains no separator, and does not start or end with a
// character used in a separator, since that could form a separator across
// the boundary (e.g. element "-" makes "0__---0" in the custom preset).
func validPart(n *Naming, p string, re *regexp.Regexp) bool {
	if p == "" || !re.MatchString(p) {
		return false
	}
	for _, sep := range []string{n.ElementSep(), n.ModifierSep(), n.ValueSep()} {
		if sep == "" {
			continue
		}
		if strings.Contains(p, sep) ||
			strings.ContainsAny(p[:1], sep) || strings.ContainsAny(p[len(p)-1:], sep) {
			return false
		}
	}
	return true
}

// FuzzParseName checks, for every preset, that Parse never panics, that
// Base+ModSuffix gives back the input, that Format rebuilds valid names,
// and that every suggestion is itself a valid name.
func FuzzParseName(f *testing.F) {
	for _, s := range []string{"", "b", "b__e--m", "b--k_v", "B-e_m_v", "____", "--", "_-_-", "b__e__f--m_v_w", "é__ü"} {
		f.Add(s)
	}
	namings := allNamings(f)

	f.Fuzz(func(t *testing.T, class string) {
		for _, n := range namings {
			nm := n.Parse(class)
			if nm.Base()+nm.ModSuffix() != class {
				t.Fatalf("%s: Base %q + ModSuffix %q != %q", n.Preset(), nm.Base(), nm.ModSuffix(), class)
			}
			if nm.Valid() {
				if got := n.Format(nm.Block, nm.Elements, nm.Mod, nm.Val); got != class {
					t.Fatalf("%s: Format(Parse(%q)) = %q", n.Preset(), class, got)
				}
			}
			if s := n.Suggest(nm); s != "" && !n.Parse(s).Valid() {
				t.Fatalf("%s: suggestion %q for %q is invalid", n.Preset(), s, class)
			}
		}
	})
}

// FuzzFormatParse builds names from parts, so every input is valid by
// construction, and checks that Parse(Format(parts)) gives the parts back.
func FuzzFormatParse(f *testing.F) {
	f.Add("card", "title", "size", "lg")
	f.Add("card", "", "featured", "")
	f.Add("Card", "title", "size", "lg")
	f.Add("b", "", "", "")
	f.Add("é", "ü", "m", "v")
	namings := allNamings(f)

	f.Fuzz(func(t *testing.T, block, elem, mod, val string) {
		for _, n := range namings {
			if !validPart(n, block, n.block) ||
				(elem != "" && !validPart(n, elem, n.elem)) ||
				(mod != "" && !validPart(n, mod, n.mod)) ||
				(val != "" && (mod == "" || n.ValueSep() == "" || !validPart(n, val, n.val))) {
				continue
			}
			var elems []string
			if elem != "" {
				elems = []string{elem}
			}
			s := n.Format(block, elems, mod, val)
			nm := n.Parse(s)
			if !nm.Valid() || nm.Block != block || !slices.Equal(nm.Elements, elems) ||
				nm.Mod != mod || nm.Val != val {
				t.Fatalf("%s: Parse(Format(%q, %q, %q, %q)) = %+v", n.Preset(), block, elem, mod, val, nm)
			}
		}
	})
}

package bem

import "testing"

// FuzzParseName checks, for every preset, that Parse never panics, that
// Base+ModSuffix gives back the input, and that Format rebuilds valid names.
func FuzzParseName(f *testing.F) {
	for _, s := range []string{"", "b", "b__e--m", "b--k_v", "B-e_m_v", "____", "--", "_-_-", "b__e__f--m_v_w", "é__ü"} {
		f.Add(s)
	}
	var namings []*Naming
	for _, p := range []string{PresetClassic, PresetKV, PresetReact, PresetCustom} {
		n, err := NewNaming(NamingConfig{Preset: p})
		if err != nil {
			f.Fatal(err)
		}
		namings = append(namings, n)
	}
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

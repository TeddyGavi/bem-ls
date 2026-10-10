package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"scss-bem-gen/internal/bem"
)

func TestParse(t *testing.T) {
	for _, raw := range []string{
		`{"naming":{"preset":"react"},"generator":{"indent":"tab"}}`,
		`{"bem":{"naming":{"preset":"react"},"generator":{"indent":"tab"}}}`,
	} {
		s, err := Parse(json.RawMessage(raw), true)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if s.Naming.Preset != "react" || s.Generator.Indent != "tab" {
			t.Errorf("%s: %+v", raw, s)
		}
	}
	s, err := Parse(json.RawMessage(`{"generator":{"indent":4},"rules":{"invalidName":"error"}}`), true)
	if err != nil || s.Generator.Indent != "4" || s.Rules[bem.RuleInvalidName] != bem.SeverityError {
		t.Errorf("indent/rules: %+v %v", s, err)
	}
	if _, err := Parse(json.RawMessage(`{"namng":{}}`), true); err == nil || !strings.Contains(err.Error(), `unknown field "namng"`) {
		t.Errorf("strict parse: %v", err)
	}
	if _, err := Parse(json.RawMessage(`{"namng":{},"server":{"path":"x"}}`), false); err != nil {
		t.Errorf("lenient parse: %v", err)
	}
	if _, err := Parse(json.RawMessage(`{"rules":{"invalidName":"loud"}}`), false); err == nil {
		t.Error("bad severity accepted")
	}
	for _, empty := range []string{"", "null", "[]", "{}", `{"bem": []}`} {
		if s, err := Parse(json.RawMessage(empty), true); err != nil || !reflect.DeepEqual(s, Settings{}) {
			t.Errorf("empty %q: %+v %v", empty, s, err)
		}
	}
}

func TestMerge(t *testing.T) {
	base := `{"bem":{"naming":{"preset":"kv","block":"x"},"rules":{"invalidName":"error","nestedElement":"off"},"ignore":["^a"]}}`
	over := `{"naming":{"preset":"react"},"rules":{"invalidName":"hint"},"ignore":["^b"]}`
	got, err := Merge(json.RawMessage(base), json.RawMessage(over))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"ignore":["^b"],"naming":{"block":"x","preset":"react"},"rules":{"invalidName":"hint","nestedElement":"off"}}`
	if string(got) != want {
		t.Errorf("Merge:\n got %s\nwant %s", got, want)
	}
	if got, _ := Merge(nil, json.RawMessage(over)); !strings.Contains(string(got), `"react"`) {
		t.Errorf("Merge(nil, over) = %s", got)
	}
	if got, _ := Merge(json.RawMessage(base), nil); !strings.Contains(string(got), `"kv"`) {
		t.Errorf("Merge(base, nil) = %s", got)
	}
	if _, err := Merge(json.RawMessage(`{`), nil); err == nil {
		t.Error("bad JSON merged")
	}
}

func TestLoadFileAndFind(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	sub := filepath.Join(root, "src", "a")
	os.MkdirAll(sub, 0o755)
	if got := Find(sub); got != "" {
		t.Errorf("Find without a file = %q", got)
	}
	cfg := filepath.Join(root, FileName)
	os.WriteFile(cfg, []byte("\xef\xbb\xbf{\"$schema\": \"./schema.json\", \"naming\": {\"preset\": \"kv\"}}"), 0o644)
	if got := Find(sub); got != cfg {
		t.Errorf("Find = %q, want %q", got, cfg)
	}
	raw, err := LoadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := Parse(raw, true); s.Naming.Preset != "kv" {
		t.Errorf("loaded %+v", s)
	}

	// A project root (.git) stops the search: parents aren't consulted.
	inner := filepath.Join(root, "nested")
	os.MkdirAll(filepath.Join(inner, ".git"), 0o755)
	if got := Find(inner); got != "" {
		t.Errorf("Find crossed a project root: %q", got)
	}

	for content, wantOff := range map[string]int{
		`{"naming": {"preset": "kv",}}`:          27,
		`{"naming": {"prset": "kv"}}`:            -1,
		`{"misc": {"diagnosticsDelay": "fast"}}`: 36, // end of the bad value
		`[1, 2]`:                                 1,
	} {
		os.WriteFile(cfg, []byte(content), 0o644)
		_, err := LoadFile(cfg)
		var fe *FileError
		if !errors.As(err, &fe) {
			t.Errorf("%s: error %v is not a FileError", content, err)
			continue
		}
		if fe.Offset != wantOff {
			t.Errorf("%s: offset %d, want %d (%v)", content, fe.Offset, wantOff, fe.Err)
		}
	}
	os.WriteFile(cfg, []byte("  \n"), 0o644)
	if raw, err := LoadFile(cfg); err != nil || raw != nil {
		t.Errorf("empty file: %s %v", raw, err)
	}
}

func TestGeneratorOptions(t *testing.T) {
	n := bem.Classic()
	for indent, want := range map[Indent]string{"": "", "tab": "\t", "4": "    "} {
		o, err := Settings{Generator: GeneratorSettings{Indent: indent}}.GeneratorOptions(n)
		if err != nil || o.Indent != want {
			t.Errorf("indent %q: %q %v", indent, o.Indent, err)
		}
	}
	if _, err := (Settings{Generator: GeneratorSettings{Indent: "99"}}).GeneratorOptions(n); err == nil {
		t.Error("indent 99 accepted")
	}
	if _, err := (Settings{Generator: GeneratorSettings{Nesting: "tree"}}).GeneratorOptions(n); err == nil {
		t.Error("nesting tree accepted")
	}
}

// TestSchemaMatchesSettings keeps schema/bem.config.schema.json in step
// with the Settings struct: same properties at every level, and the enums
// list every rule, preset and severity.
func TestSchemaMatchesSettings(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "schema", "bem.config.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	var check func(path string, typ reflect.Type, node map[string]any)
	check = func(path string, typ reflect.Type, node map[string]any) {
		props, _ := node["properties"].(map[string]any)
		var structKeys, schemaKeys []string
		fields := map[string]reflect.Type{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			structKeys = append(structKeys, name)
			fields[name] = f.Type
		}
		for k := range props {
			schemaKeys = append(schemaKeys, k)
		}
		sort.Strings(structKeys)
		sort.Strings(schemaKeys)
		if !reflect.DeepEqual(structKeys, schemaKeys) {
			t.Errorf("%s: struct fields %v, schema properties %v", path, structKeys, schemaKeys)
		}
		for name, ft := range fields {
			if ft.Kind() == reflect.Struct {
				sub, _ := props[name].(map[string]any)
				check(path+"."+name, ft, sub)
			}
		}
	}
	check("settings", reflect.TypeOf(Settings{}), schema)

	props := schema["properties"].(map[string]any)
	rules := props["rules"].(map[string]any)["properties"].(map[string]any)
	for _, r := range bem.Rules {
		if _, ok := rules[string(r)]; !ok {
			t.Errorf("schema lacks rule %s", r)
		}
	}
	if len(rules) != len(bem.Rules) {
		t.Errorf("schema has %d rules, bem has %d", len(rules), len(bem.Rules))
	}
	presets := props["naming"].(map[string]any)["properties"].(map[string]any)["preset"].(map[string]any)["enum"].([]any)
	for _, p := range []string{bem.PresetClassic, bem.PresetKV, bem.PresetReact, bem.PresetCustom} {
		if !containsAny(presets, p) {
			t.Errorf("schema lacks preset %s", p)
		}
	}
	sev := schema["definitions"].(map[string]any)["severity"].(map[string]any)["enum"].([]any)
	for _, s := range []bem.Severity{bem.SeverityOff, bem.SeverityHint, bem.SeverityInfo, bem.SeverityWarning, bem.SeverityError} {
		if !containsAny(sev, s.String()) {
			t.Errorf("schema lacks severity %s", s)
		}
	}
}

func containsAny(list []any, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

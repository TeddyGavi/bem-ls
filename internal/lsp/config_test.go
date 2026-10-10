package lsp

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"scss-bem-gen/internal/lsp/protocol"
)

// diagCodes waits until the diagnostics of rel satisfy ok.
func (c *client) diagsUntil(rel string, ok func(string) bool) string {
	c.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		got := strings.Join(c.waitDiags(rel, 0), " ")
		if ok(got) {
			return got
		}
		select {
		case <-c.diagCh:
		case <-deadline:
			c.t.Fatalf("%s: diagnostics never matched; last %q", rel, got)
		}
	}
}

func TestConfigFileWinsOverEditorSettings(t *testing.T) {
	c := newClientWith(t, clientOptions{
		initOptions: map[string]any{"rules": map[string]string{"invalidName": "error", "missingInStylesheet": "off"}},
		files:       map[string]string{"bem.config.json": `{"$schema": "x", "rules": {"invalidName": "off"}}`},
	})
	c.open("src/Card/Card.tsx")
	// invalidName is off (file wins); missingInStylesheet stays off (editor).
	if got := strings.Join(c.waitDiags("src/Card/Card.tsx", 1), " "); got != "" {
		t.Errorf("diagnostics %q", got)
	}
}

func TestConfigFileErrorsAndReload(t *testing.T) {
	c := newClientWith(t, clientOptions{
		files: map[string]string{"bem.config.json": "{\n  \"naming\": {\"preset\": \"kv\",}\n}\n"},
	})
	got := c.diagsUntil("bem.config.json", func(s string) bool { return s != "" })
	if got != "1:28 " { // the stray '}' after "kv",
		t.Errorf("config diagnostics %q", got)
	}
	c.waitMessage("bem.config.json: invalid character")
	// The broken file is ignored: defaults apply.
	c.open("src/Card/Card.tsx")
	c.diagsUntil("src/Card/Card.tsx", func(s string) bool { return strings.Contains(s, "invalidName") })

	// Fix it; the watcher reports the change.
	os.WriteFile(c.path("bem.config.json"), []byte(`{"rules": {"invalidName": "off", "missingInStylesheet": "off"}}`), 0o644)
	c.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": []any{
		map[string]any{"uri": c.uri("bem.config.json"), "type": protocol.FileChanged},
	}})
	c.diagsUntil("bem.config.json", func(s string) bool { return s == "" })
	c.diagsUntil("src/Card/Card.tsx", func(s string) bool { return s == "" })

	// Invalid values (not syntax) are reported too.
	os.WriteFile(c.path("bem.config.json"), []byte(`{"naming": {"preset": "bootstrap"}}`), 0o644)
	c.notify("textDocument/didSave", map[string]any{"textDocument": map[string]any{"uri": c.uri("bem.config.json")}})
	c.waitMessage("invalid bem.config.json: unknown naming preset")
}

// waitMessage waits for a window/showMessage containing want.
func (c *client) waitMessage(want string) {
	c.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		c.mu.Lock()
		msgs := strings.Join(c.msgs, "\n")
		c.mu.Unlock()
		if strings.Contains(msgs, want) {
			return
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("no message containing %q; got:\n%s", want, msgs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPullConfiguration(t *testing.T) {
	section := json.RawMessage(`{"rules": {"invalidName": "off"}, "misc": {"diagnosticsDelay": 5}, "server": {"path": "ignored"}}`)
	c := newClientWith(t, clientOptions{configuration: section, noInitOptions: true})
	c.open("src/Card/Card.tsx")
	c.diagsUntil("src/Card/Card.tsx", func(s string) bool { return s != "" && !strings.Contains(s, "invalidName") })

	// An empty didChangeConfiguration means "pull again".
	c.mu.Lock()
	c.configSection = json.RawMessage(`{"rules": {"invalidName": "error", "missingInStylesheet": "off"}}`)
	c.mu.Unlock()
	c.notify("workspace/didChangeConfiguration", map[string]any{"settings": nil})
	c.diagsUntil("src/Card/Card.tsx", func(s string) bool { return s == "5:32 invalidName" })
	c.mu.Lock()
	calls := c.configCalls
	c.mu.Unlock()
	if calls != 2 {
		t.Errorf("workspace/configuration called %d times, want 2", calls)
	}
}

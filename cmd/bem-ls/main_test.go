package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestBinaryOverStdio builds bem-ls, runs a session over real pipes, and
// checks that standard output carries nothing but protocol messages.
func TestBinaryOverStdio(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bem-ls")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	root := filepath.Join(dir, "ws")
	os.MkdirAll(root, 0o755)
	comp := filepath.Join(root, "Card.tsx")
	os.WriteFile(comp, []byte(`<div className="card"><p className="card__x"/></div>`), 0o644)
	os.WriteFile(filepath.Join(root, "Card.scss"), []byte(".card {}\n"), 0o644)

	cmd := exec.Command(bin, "--stdio")
	stdin, _ := cmd.StdinPipe()
	stdoutPipe, _ := cmd.StdoutPipe()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	stdout := bufio.NewReader(io.TeeReader(stdoutPipe, &raw))

	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(stdin, "Content-Length: %d\r\n\r\n%s", len(b), b)
	}
	read := func() map[string]any {
		n := 0
		for {
			line, err := stdout.ReadString('\n')
			if err != nil {
				t.Fatalf("reading: %v (stderr: %s)", err, stderr.String())
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if _, err := fmt.Sscanf(line, "Content-Length: %d", &n); err != nil {
				t.Fatalf("non-protocol output on stdout: %q", line)
			}
		}
		b := make([]byte, n)
		io.ReadFull(stdout, b)
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("bad JSON on stdout: %q", b)
		}
		return m
	}
	fileURI := func(p string) string {
		p = filepath.ToSlash(p)
		if !strings.HasPrefix(p, "/") {
			p = "/" + p // C:/x on Windows
		}
		return (&url.URL{Scheme: "file", Path: p}).String()
	}
	uri, compURI := fileURI(root), fileURI(comp)

	send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"rootUri": uri, "capabilities": map[string]any{},
		"initializationOptions": map[string]any{"misc": map[string]any{"diagnosticsDelay": 1}},
	}})
	if m := read(); m["id"] != float64(1) || m["result"] == nil {
		t.Fatalf("initialize: %v", m)
	}
	send(map[string]any{"jsonrpc": "2.0", "method": "initialized", "params": map[string]any{}})
	b, _ := os.ReadFile(comp)
	send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{
		"textDocument": map[string]any{"uri": compURI, "languageId": "typescriptreact", "version": 1, "text": string(b)},
	}})
	deadline := time.Now().Add(5 * time.Second)
	for {
		m := read()
		if m["method"] == "textDocument/publishDiagnostics" {
			s, _ := json.Marshal(m["params"])
			if !strings.Contains(string(s), "missingInStylesheet") {
				t.Errorf("diagnostics: %s", s)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no diagnostics")
		}
	}
	send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "shutdown"})
	for {
		if m := read(); m["id"] == float64(2) {
			break
		}
	}
	send(map[string]any{"jsonrpc": "2.0", "method": "exit"})
	done := make(chan error, 1)
	go func() {
		io.Copy(io.Discard, stdout)
		done <- cmd.Wait()
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("exit status: %v (stderr: %s)", err, stderr.String())
		}
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("server did not exit")
	}
	if !strings.Contains(stderr.String(), "initialized: root=") {
		t.Errorf("expected logs on stderr, got %q", stderr.String())
	}
}

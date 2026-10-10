package jsonrpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// pipeConn starts a Conn with handler and returns a writer to it and a
// reader of what it writes.
func pipeConn(t *testing.T, handler Handler) (*Conn, io.WriteCloser, *bufio.Reader, chan error) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := NewConn(inR, outW, handler, nil)
	done := make(chan error, 1)
	go func() {
		done <- c.Run(context.Background())
		outW.Close()
	}()
	t.Cleanup(func() { inW.Close() })
	return c, inW, bufio.NewReader(outR), done
}

func send(t *testing.T, w io.Writer, body string) {
	t.Helper()
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
		t.Fatal(err)
	}
}

func recv(t *testing.T, r *bufio.Reader) map[string]any {
	t.Helper()
	ch := make(chan map[string]any, 1)
	go func() {
		n := 0
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				ch <- nil
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			fmt.Sscanf(line, "Content-Length: %d", &n)
		}
		body := make([]byte, n)
		io.ReadFull(r, body)
		var m map[string]any
		json.Unmarshal(body, &m)
		ch <- m
	}()
	select {
	case m := <-ch:
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("no message from server")
		return nil
	}
}

func TestRequestResponse(t *testing.T) {
	_, w, r, _ := pipeConn(t, func(ctx context.Context, req *Request) (any, error) {
		switch req.Method {
		case "echo":
			var v any
			json.Unmarshal(req.Params, &v)
			return v, nil
		case "nothing":
			return nil, nil
		}
		return nil, ErrMethodNotFound
	})
	send(t, w, `{"jsonrpc":"2.0","id":1,"method":"echo","params":{"a":1}}`)
	if m := recv(t, r); m["id"] != float64(1) || m["result"].(map[string]any)["a"] != float64(1) {
		t.Errorf("echo: %v", m)
	}
	send(t, w, `{"jsonrpc":"2.0","id":"s","method":"nothing"}`)
	if m := recv(t, r); m["id"] != "s" || m["result"] != nil {
		t.Errorf("nothing: %v", m)
	} else if _, has := m["result"]; !has {
		t.Errorf("result member missing: %v", m)
	}
	send(t, w, `{"jsonrpc":"2.0","id":2,"method":"nope"}`)
	if m := recv(t, r); m["error"].(map[string]any)["code"] != float64(CodeMethodNotFound) {
		t.Errorf("nope: %v", m)
	}
	send(t, w, `{not json`)
	if m := recv(t, r); m["error"].(map[string]any)["code"] != float64(CodeParseError) {
		t.Errorf("bad json: %v", m)
	}
}

func TestCancel(t *testing.T) {
	started := make(chan struct{})
	_, w, r, _ := pipeConn(t, func(ctx context.Context, req *Request) (any, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	send(t, w, `{"jsonrpc":"2.0","id":7,"method":"slow"}`)
	<-started
	send(t, w, `{"jsonrpc":"2.0","method":"$/cancelRequest","params":{"id":7}}`)
	m := recv(t, r)
	if m["id"] != float64(7) || m["error"].(map[string]any)["code"] != float64(CodeRequestCancelled) {
		t.Errorf("cancelled: %v", m)
	}
}

func TestPanicIsReported(t *testing.T) {
	_, w, r, _ := pipeConn(t, func(ctx context.Context, req *Request) (any, error) {
		panic("boom")
	})
	send(t, w, `{"jsonrpc":"2.0","id":1,"method":"x"}`)
	m := recv(t, r)
	if m["error"].(map[string]any)["code"] != float64(CodeInternalError) {
		t.Errorf("panic: %v", m)
	}
}

func TestCallAndNotify(t *testing.T) {
	var c *Conn
	result := make(chan string, 1)
	c, w, r, _ := pipeConn(t, func(ctx context.Context, req *Request) (any, error) {
		if req.Method == "go" {
			go func() {
				var got string
				err := c.Call(context.Background(), "client/ask", map[string]int{"n": 1}, &got)
				if err != nil {
					got = "error: " + err.Error()
				}
				result <- got
			}()
		}
		return nil, nil
	})
	send(t, w, `{"jsonrpc":"2.0","method":"go"}`)
	m := recv(t, r)
	if m["method"] != "client/ask" {
		t.Fatalf("expected a call, got %v", m)
	}
	send(t, w, fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"result":"yes"}`, m["id"]))
	if got := <-result; got != "yes" {
		t.Errorf("call result %q", got)
	}
	go c.Notify("note", map[string]int{"x": 1}) // io.Pipe blocks until read
	if m := recv(t, r); m["method"] != "note" || m["id"] != nil {
		t.Errorf("notify: %v", m)
	}
}

func TestNotificationsAreOrdered(t *testing.T) {
	var mu sync.Mutex
	var got []string
	_, w, r, _ := pipeConn(t, func(ctx context.Context, req *Request) (any, error) {
		if req.IsNotification() {
			time.Sleep(time.Millisecond) // would reorder if run concurrently
			mu.Lock()
			got = append(got, req.Method)
			mu.Unlock()
			return nil, nil
		}
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(got, ","), nil
	})
	for i := 0; i < 5; i++ {
		send(t, w, fmt.Sprintf(`{"jsonrpc":"2.0","method":"n%d"}`, i))
	}
	send(t, w, `{"jsonrpc":"2.0","id":1,"method":"list"}`)
	if m := recv(t, r); m["result"] != "n0,n1,n2,n3,n4" {
		t.Errorf("order: %v", m["result"])
	}
}

func TestRunEndsOnEOFAndClose(t *testing.T) {
	_, w, _, done := pipeConn(t, func(ctx context.Context, req *Request) (any, error) { return nil, nil })
	w.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return on EOF")
	}

	var c *Conn
	c, w2, _, done2 := pipeConn(t, func(ctx context.Context, req *Request) (any, error) {
		c.Close()
		return nil, nil
	})
	send(t, w2, `{"jsonrpc":"2.0","method":"exit"}`)
	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after Close")
	}
}

func TestBadHeader(t *testing.T) {
	_, w, _, done := pipeConn(t, func(ctx context.Context, req *Request) (any, error) { return nil, nil })
	io.WriteString(w, "Content-Length: nope\r\n\r\n")
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "Content-Length") {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not fail on a bad header")
	}
}

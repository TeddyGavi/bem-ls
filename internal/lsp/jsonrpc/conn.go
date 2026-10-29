// Package jsonrpc implements JSON-RPC 2.0 over a stream with LSP's
// Content-Length framing.
//
// Notifications are handled one at a time, in order, on the reading
// goroutine, so document changes are applied before any later request
// sees them. Each request runs on its own goroutine with a context that
// "$/cancelRequest" cancels.
package jsonrpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/TeddyGavi/bem-ls/internal/core"
)

// Error codes from JSON-RPC and LSP.
const (
	CodeParseError           = -32700
	CodeInvalidRequest       = -32600
	CodeMethodNotFound       = -32601
	CodeInvalidParams        = -32602
	CodeInternalError        = -32603
	CodeServerNotInitialized = -32002
	CodeRequestCancelled     = -32800
	CodeContentModified      = -32801
	CodeRequestFailed        = -32803
)

// Error is a JSON-RPC error response.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message) }

// Errorf returns an *Error with the given code.
func Errorf(code int, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// ErrMethodNotFound is returned by handlers for methods they don't know.
var ErrMethodNotFound = Errorf(CodeMethodNotFound, "method not found")

// Request is an incoming request (ID set) or notification (ID nil).
type Request struct {
	ID     *json.RawMessage
	Method string
	Params json.RawMessage
}

// IsNotification reports whether the request expects no response.
func (r *Request) IsNotification() bool { return r.ID == nil }

// Handler handles incoming messages. For notifications the result is
// ignored. Returning ErrMethodNotFound for an unknown notification is fine;
// it is not reported.
type Handler func(ctx context.Context, req *Request) (any, error)

type wireMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *Error           `json:"error,omitempty"`
}

// response is written with "result" always present (null on success with
// no value), as JSON-RPC requires.
type response struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Result  any              `json:"result"`
}

type errorResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Error   *Error           `json:"error"`
}

// Conn is a JSON-RPC connection.
type Conn struct {
	r       *bufio.Reader
	w       io.Writer
	handler Handler
	log     *core.Logger

	writeMu sync.Mutex

	mu        sync.Mutex
	cancels   map[string]context.CancelFunc // incoming requests in flight
	pending   map[string]chan wireMessage   // outgoing calls awaiting replies
	nextID    atomic.Int64
	closed    bool
	requestWG sync.WaitGroup
}

// NewConn returns a connection reading from r and writing to w. logger may
// be nil.
func NewConn(r io.Reader, w io.Writer, handler Handler, logger *core.Logger) *Conn {
	if logger == nil {
		logger = core.NewLogger("jsonrpc")
		logger.SetOutput(io.Discard)
	}
	return &Conn{
		r:       bufio.NewReader(r),
		w:       w,
		handler: handler,
		log:     logger,
		cancels: map[string]context.CancelFunc{},
		pending: map[string]chan wireMessage{},
	}
}

// Run reads and dispatches messages until the stream ends, ctx is cancelled
// or a handler calls Close. It waits for running requests before returning.
func (c *Conn) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer c.requestWG.Wait()
	defer c.failPending()
	for {
		body, err := c.readMessage()
		if err != nil {
			if c.isClosed() || errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		var msg wireMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			c.log.Infof("jsonrpc: bad message: %v", err)
			c.replyError(nil, Errorf(CodeParseError, "parse error: %v", err))
			continue
		}
		switch {
		case msg.Method == "" && msg.ID != nil:
			c.deliverResponse(msg)
		case msg.Method == "":
			c.replyError(nil, Errorf(CodeInvalidRequest, "message has no method"))
		case msg.ID == nil:
			c.handleNotification(ctx, &Request{Method: msg.Method, Params: msg.Params})
		default:
			c.startRequest(ctx, &Request{ID: msg.ID, Method: msg.Method, Params: msg.Params})
		}
		if c.isClosed() {
			return nil
		}
	}
}

// Close makes Run return after the current message.
func (c *Conn) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
}

func (c *Conn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *Conn) handleNotification(ctx context.Context, req *Request) {
	if req.Method == "$/cancelRequest" {
		var p struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(req.Params, &p) == nil {
			c.mu.Lock()
			if cancel, ok := c.cancels[string(p.ID)]; ok {
				cancel()
			}
			c.mu.Unlock()
		}
		return
	}
	defer c.recoverPanic(req, nil)
	if _, err := c.handler(ctx, req); err != nil && err != ErrMethodNotFound {
		c.log.Infof("jsonrpc: notification %s: %v", req.Method, err)
	}
}

func (c *Conn) startRequest(ctx context.Context, req *Request) {
	key := string(*req.ID)
	rctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.cancels[key] = cancel
	c.mu.Unlock()
	c.requestWG.Add(1)
	go func() {
		defer c.requestWG.Done()
		defer func() {
			cancel()
			c.mu.Lock()
			delete(c.cancels, key)
			c.mu.Unlock()
		}()
		defer c.recoverPanic(req, req.ID)
		result, err := c.handler(rctx, req)
		if err == nil && rctx.Err() != nil && ctx.Err() == nil {
			err = Errorf(CodeRequestCancelled, "request cancelled")
		}
		if errors.Is(err, context.Canceled) && rctx.Err() != nil {
			err = Errorf(CodeRequestCancelled, "request cancelled")
		}
		if err != nil {
			var rpcErr *Error
			if !errors.As(err, &rpcErr) {
				rpcErr = Errorf(CodeRequestFailed, "%v", err)
			}
			c.replyError(req.ID, rpcErr)
			return
		}
		c.write(response{JSONRPC: "2.0", ID: req.ID, Result: result})
	}()
}

func (c *Conn) recoverPanic(req *Request, id *json.RawMessage) {
	if r := recover(); r != nil {
		c.log.Infof("jsonrpc: panic in %s: %v\n%s", req.Method, r, debug.Stack())
		if id != nil {
			c.replyError(id, Errorf(CodeInternalError, "internal error: %v", r))
		}
	}
}

func (c *Conn) replyError(id *json.RawMessage, e *Error) {
	if id == nil {
		null := json.RawMessage("null")
		id = &null
	}
	c.write(errorResponse{JSONRPC: "2.0", ID: id, Error: e})
}

// Notify sends a notification to the other side.
func (c *Conn) Notify(method string, params any) error {
	return c.write(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{"2.0", method, params})
}

// Call sends a request to the other side and waits for the reply, which is
// decoded into result (if not nil).
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	id := json.RawMessage(strconv.FormatInt(c.nextID.Add(1), 10))
	ch := make(chan wireMessage, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("jsonrpc: connection closed")
	}
	c.pending[string(id)] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, string(id))
		c.mu.Unlock()
	}()
	err := c.write(struct {
		JSONRPC string           `json:"jsonrpc"`
		ID      *json.RawMessage `json:"id"`
		Method  string           `json:"method"`
		Params  any              `json:"params,omitempty"`
	}{"2.0", &id, method, params})
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		c.Notify("$/cancelRequest", map[string]any{"id": id})
		return ctx.Err()
	case msg, ok := <-ch:
		if !ok {
			return errors.New("jsonrpc: connection closed")
		}
		if msg.Error != nil {
			return msg.Error
		}
		if result != nil && len(msg.Result) > 0 {
			return json.Unmarshal(msg.Result, result)
		}
		return nil
	}
}

func (c *Conn) deliverResponse(msg wireMessage) {
	c.mu.Lock()
	ch, ok := c.pending[string(*msg.ID)]
	c.mu.Unlock()
	if ok {
		ch <- msg
	} else {
		c.log.Infof("jsonrpc: response for unknown id %s", *msg.ID)
	}
}

func (c *Conn) failPending() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

// ---------------------------------------------------------------------------
// Framing

const maxMessageSize = 64 << 20

func (c *Conn) readMessage() ([]byte, error) {
	tp := textproto.NewReader(c.r)
	header, err := tp.ReadMIMEHeader()
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("jsonrpc: reading header: %w", err)
	}
	cl := strings.TrimSpace(header.Get("Content-Length"))
	n, err := strconv.Atoi(cl)
	if err != nil || n < 0 {
		return nil, fmt.Errorf("jsonrpc: bad Content-Length %q", cl)
	}
	if n > maxMessageSize {
		return nil, fmt.Errorf("jsonrpc: message of %d bytes is too large", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(c.r, body); err != nil {
		return nil, fmt.Errorf("jsonrpc: reading body: %w", err)
	}
	return body, nil
}

func (c *Conn) write(msg any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		c.log.Infof("jsonrpc: marshal: %v", err)
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = c.w.Write(body)
	return err
}

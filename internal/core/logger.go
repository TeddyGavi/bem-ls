package core

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Logger writes timestamped messages to standard error. It never writes to
// standard output, which carries the generated SCSS in the CLI and the
// protocol stream in the language server.
type Logger struct {
	Name string

	mu  sync.Mutex
	out io.Writer
}

// NewLogger returns a Logger writing to standard error.
func NewLogger(name string) *Logger {
	return &Logger{Name: name, out: os.Stderr}
}

// SetOutput redirects the logger, e.g. to a file or io.Discard.
func (l *Logger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.out = w
}

// Info logs an informational message.
func (l *Logger) Info(msg string) { l.log("INFO", msg, nil) }

// Error logs msg together with err.
func (l *Logger) Error(err error, msg string) { l.log("ERROR", msg, err) }

func (l *Logger) log(level, msg string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ts := time.Now().Format(time.RFC3339)
	if err != nil {
		fmt.Fprintf(l.out, "%s [%s] [%s] %s: %v\n", ts, level, l.Name, msg, err)
	} else {
		fmt.Fprintf(l.out, "%s [%s] [%s] %s\n", ts, level, l.Name, msg)
	}
}

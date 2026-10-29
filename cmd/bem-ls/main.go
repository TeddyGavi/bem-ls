// Command bem-ls is a language server for BEM class names in JSX/TSX and
// their SCSS/CSS stylesheets. It speaks LSP over standard input and output.
//
// Usage:
//
//	bem-ls [-log file] [--stdio]
//
// Logs go to standard error (or the -log file), never to standard output,
// which carries the protocol.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/TeddyGavi/bem-ls/internal/core"
	"github.com/TeddyGavi/bem-ls/internal/lsp"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	fs := flag.NewFlagSet("bem-ls", flag.ContinueOnError)
	logFile := fs.String("log", "", "write logs to this file instead of standard error")
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.Bool("stdio", true, "use standard input/output (the only transport; accepted for editor compatibility)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if *showVersion {
		fmt.Println("bem-ls", version)
		return
	}

	logger := core.NewLogger("bem-ls")
	if *logFile != "" {
		f, err := os.OpenFile(*logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bem-ls: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		logger.SetOutput(f)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := lsp.NewServer(version, logger)
	if err := srv.Run(ctx, os.Stdin, os.Stdout); err != nil {
		logger.Errorf(err, "exit: %v", err)
		os.Exit(1)
	}
}

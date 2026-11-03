// Command wallet runs the e-wallet service (TDR-001) and its maintenance sub-commands.
//
// Usage:
//
//	wallet [serve]          run the HTTP API (default)
//	wallet migrate up       apply pending database migrations
//	wallet migrate status   print the schema version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // Africa/Cairo must resolve in distroless images (TDR-012)
)

// Exit codes.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const usage = `usage:
  wallet [serve]          run the HTTP API (default)
  wallet migrate up       apply pending database migrations
  wallet migrate status   print the schema version
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], nil, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run executes one sub-command and returns the process exit code. A nil environ reads
// the process environment.
func run(ctx context.Context, args []string, environ map[string]string, stdout, stderr io.Writer) int {
	root := newFlagSet("wallet", stderr)
	if err := root.Parse(args); err != nil {
		return parseExit(err, stdout, stderr)
	}
	command, rest := "serve", root.Args()
	if len(rest) > 0 {
		command, rest = rest[0], rest[1:]
	}

	sub := newFlagSet("wallet "+command, stderr)
	if err := sub.Parse(rest); err != nil {
		return parseExit(err, stdout, stderr)
	}
	switch command {
	case "serve":
		if sub.NArg() != 0 {
			return usageError(stderr, "serve takes no arguments")
		}
		return serve(ctx, environ, stdout, stderr)
	case "migrate":
		if sub.NArg() != 1 {
			return usageError(stderr, "migrate needs one action: up or status")
		}
		switch sub.Arg(0) {
		case "up":
			return migrateUp(ctx, environ, stdout, stderr)
		case "status":
			return migrateStatus(ctx, environ, stdout, stderr)
		default:
			return usageError(stderr, fmt.Sprintf("unknown migrate action %q", sub.Arg(0)))
		}
	default:
		return usageError(stderr, fmt.Sprintf("unknown command %q", command))
	}
}

// newFlagSet returns a flag set that reports errors instead of exiting. No flags are
// defined yet; parsing still rejects unknown flags and handles -h.
func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {} // parseExit prints the usage once, to the right stream
	return fs
}

// parseExit maps a flag parsing error to an exit code; -h prints usage and succeeds.
func parseExit(err error, stdout, stderr io.Writer) int {
	if errors.Is(err, flag.ErrHelp) {
		_, _ = fmt.Fprint(stdout, usage)
		return exitOK
	}
	_, _ = fmt.Fprint(stderr, usage) // flag already printed the error itself
	return exitUsage
}

func usageError(stderr io.Writer, msg string) int {
	_, _ = fmt.Fprintf(stderr, "wallet: %s\n\n%s", msg, usage)
	return exitUsage
}

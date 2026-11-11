// Command uvsh is the Unified Virtual Storage Hub CLI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

var errUsage = errors.New("usage error")

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("uvsh", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var level slog.Level
	fs.TextVar(&level, "log-level", slog.LevelInfo, "minimum log `level`: debug, info, warn or error")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: uvsh [-log-level level] <command> [args]")
		fmt.Fprintln(stderr, "\ncommands:")
		fmt.Fprintln(stderr, "  init       create or upgrade the catalog")
		fmt.Fprintln(stderr, "  version    print build information")
		fmt.Fprintln(stderr, "\nflags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errUsage
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))

	if fs.NArg() == 0 {
		fs.Usage()
		return errUsage
	}
	cmd, cmdArgs := fs.Arg(0), fs.Args()[1:]
	logger.DebugContext(ctx, "dispatch", "command", cmd, "args", cmdArgs)

	switch cmd {
	case "init":
		return runInit(ctx, cmdArgs, stdout, stderr)
	case "version":
		return runVersion(stdout)
	default:
		fmt.Fprintf(stderr, "uvsh: unknown command %q\n", cmd)
		fs.Usage()
		return errUsage
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args, os.Stdout, os.Stderr)
	stop()

	switch {
	case err == nil:
		return
	case errors.Is(err, errUsage):
		os.Exit(2)
	default:
		fmt.Fprintln(os.Stderr, "uvsh:", err)
		os.Exit(1)
	}
}

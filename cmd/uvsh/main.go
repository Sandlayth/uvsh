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
	"runtime"
	"runtime/debug"
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
		fmt.Fprintln(stderr, "\ncommands:\n  version    print build information")
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
	case "version":
		return runVersion(stdout)
	default:
		fmt.Fprintf(stderr, "uvsh: unknown command %q\n", cmd)
		fs.Usage()
		return errUsage
	}
}

func runVersion(stdout io.Writer) error {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return errors.New("build info unavailable")
	}
	settings := make(map[string]string, len(info.Settings))
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	fmt.Fprintf(stdout, "uvsh %s\n", info.Main.Version)
	fmt.Fprintf(stdout, "  commit:   %s (modified=%s)\n", settings["vcs.revision"], settings["vcs.modified"])
	fmt.Fprintf(stdout, "  go:       %s\n", info.GoVersion)
	fmt.Fprintf(stdout, "  platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	return nil
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

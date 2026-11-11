package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/sandlayth/uvsh/internal/catalog/backend"
)

func runInit(ctx context.Context, args []string, stdout, stderr io.Writer) (err error) {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dsn := fs.String("db", "", "catalog `dsn`, as <backend>:<location> (required; backends: "+strings.Join(backend.Supported(), ", ")+")")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: uvsh init -db dsn")
		fmt.Fprintln(stderr, "\nCreate the catalog if needed, upgrade its schema, and report its state.")
		fmt.Fprintln(stderr, "\nflags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errUsage
	}
	if *dsn == "" || fs.NArg() > 0 {
		fs.Usage()
		return errUsage
	}

	cat, err := backend.Open(ctx, *dsn)
	if err != nil {
		return err
	}

	defer func() { err = errors.Join(err, cat.Close()) }()

	info, err := cat.Info(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "catalog %s: schema v%d\n", *dsn, info.SchemaVersion)
	return nil
}

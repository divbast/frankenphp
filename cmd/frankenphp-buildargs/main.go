// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

// Command frankenphp-buildargs prints the build arguments that one app's
// binary is built with, from apps/<app>.env, and refuses a file that says
// anything but the four inputs. Every build passes what it prints, and the
// app's name as APP itself.
//
// Usage:
//
//	frankenphp-buildargs [-dir apps] APP
//
// It prints one KEY=value per line. A value may hold spaces but never a
// newline, since the file it comes from is read line by line, so each
// line is one whole build argument: read each into one --build-arg, as
// `just build-app` does.
//
// Exit codes:
//
//	0  the arguments are printed
//	1  APP is no app name, or its file is missing, unreadable or breaks a
//	   rule; stderr names the file, the line and the key of each fault
//	2  the command line is wrong
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/divbast/frankenphp/internal/appenv"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("frankenphp-buildargs", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		complain(stderr, "usage: frankenphp-buildargs [-dir apps] APP")
		flags.PrintDefaults()
	}
	dir := flags.String("dir", "apps", "the `directory` holding each app's .env file")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return 2
	}

	in, err := load(*dir, flags.Arg(0))
	if err != nil {
		complain(stderr, "%v", err)
		return 1
	}
	if _, err := io.WriteString(stdout, strings.Join(in.Args(), "\n")+"\n"); err != nil {
		complain(stderr, "write the arguments: %v", err)
		return 1
	}
	return 0
}

func load(dir, app string) (in appenv.Inputs, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return appenv.Inputs{}, fmt.Errorf("open the apps directory: %w", err)
	}
	defer func() {
		if cerr := root.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close the apps directory: %w", cerr)
		}
	}()
	return appenv.Load(root, app)
}

// complain writes to stderr after the command's name, one line per line of
// the message.
func complain(stderr io.Writer, format string, args ...any) {
	for line := range strings.Lines(fmt.Sprintf(format, args...)) {
		// A failed write to stderr leaves nowhere better to report it.
		_, _ = fmt.Fprintln(stderr, "frankenphp-buildargs:", strings.TrimSuffix(line, "\n"))
	}
}

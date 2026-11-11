// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

// Command frankenphp-manifest reads what went into a carrier's FrankenPHP
// binary from the records in the carrier, and says what moved between two
// of them. The build workflow runs it on each carrier it builds, which also
// refuses a binary that carries a file capability, and the publish job on
// the candidates and what is published, to decide whether a daily rebuild
// publishes.
//
// Usage:
//
//	frankenphp-manifest -oci ARCHIVE -arch ARCH [-app APP]
//	frankenphp-manifest -compare A.json B.json
//	frankenphp-manifest -changes -app APP [-published ARCHIVE] amd64=ARCHIVE arm64=ARCHIVE
//
// -oci prints, as JSON, the manifest of the linux/ARCH image in the OCI
// archive ARCHIVE, and with -app refuses one built for another app.
//
// -compare prints what moved from the manifest in A.json to the one in
// B.json, one change per line, and nothing when nothing did.
//
// -changes prints what moved, for each architecture, from the image
// published for it in the OCI archive given as -published to the carrier
// built for it, one change per line after the architecture, and nothing
// when nothing did. An architecture with nothing published, or every one
// when -published is left out, prints as "<arch>: not published". It wants
// a carrier for each of amd64 and arm64, and refuses any carrier or
// published image built for another app than APP.
//
// Exit codes:
//
//	0  -oci: the manifest is printed; -compare: the manifests are equal;
//	   -changes: the changes are printed, if any
//	1  -compare: the manifests differ. -oci and -changes: an archive is
//	   refused or cannot be read: its binary carries a file capability, it
//	   holds no image for the architecture or one for another, or a record
//	   is missing or unreadable; or -changes lacks a carrier for an
//	   architecture. stderr says which
//	2  the command line is wrong, or -compare cannot read a manifest
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/divbast/frankenphp/internal/manifest"
)

const name = "frankenphp-manifest"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// usageError is a wrong command line, which exits 2.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		complain(stderr, "usage:\n"+
			"  %[1]s -oci ARCHIVE -arch ARCH [-app APP]\n"+
			"  %[1]s -compare A.json B.json\n"+
			"  %[1]s -changes -app APP [-published ARCHIVE] amd64=ARCHIVE arm64=ARCHIVE", name)
		flags.PrintDefaults()
	}
	oci := flags.String("oci", "", "print the manifest of the OCI archive `ARCHIVE`")
	arch := flags.String("arch", "", "with -oci, the `ARCH`itecture of the image to read: amd64 or arm64")
	app := flags.String("app", "", "the `APP` every carrier must be built for")
	compare := flags.Bool("compare", false, "print what moved between the two manifest files given")
	changes := flags.Bool("changes", false, "print what moved between each ARCH=ARCHIVE carrier and what is published")
	published := flags.String("published", "", "with -changes, the published index as an OCI `ARCHIVE`, if anything is published")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	var code int
	var err error
	switch modes := countTrue(*oci != "", *compare, *changes); {
	case modes != 1:
		err = usageError{"give exactly one of -oci, -compare and -changes"}
	case *oci != "":
		err = printManifest(stdout, *oci, *arch, *app, flags.Args())
	case *compare:
		code, err = printCompare(stdout, flags.Args())
	default:
		err = printChanges(stdout, *app, *published, flags.Args())
	}
	var usage usageError
	switch {
	case errors.As(err, &usage):
		complain(stderr, "%v", err)
		flags.Usage()
		return 2
	case err != nil:
		complain(stderr, "%v", err)
		if *compare {
			return 2
		}
		return 1
	}
	return code
}

func countTrue(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

func printManifest(stdout io.Writer, archive, arch, app string, rest []string) error {
	switch {
	case arch == "":
		return usageError{"-oci needs -arch"}
	case len(rest) > 0:
		return usageError{fmt.Sprintf("unexpected argument %q", rest[0])}
	}
	m, err := read(archive, arch, app)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\n", data)
	return err
}

// read reads the arch manifest from archive, and refuses one built for
// another app than app, when app is given.
func read(archive, arch, app string) (manifest.Manifest, error) {
	m, err := manifest.FromOCIArchive(archive, arch)
	if err != nil {
		return manifest.Manifest{}, err
	}
	if app != "" && m.App != app {
		return manifest.Manifest{}, fmt.Errorf("%s: the %s image is %s's carrier, not %s's", archive, arch, m.App, app)
	}
	return m, nil
}

func printCompare(stdout io.Writer, files []string) (int, error) {
	if len(files) != 2 {
		return 0, usageError{"-compare takes two manifest files"}
	}
	var ms [2]manifest.Manifest
	for i, file := range files {
		var err error
		if ms[i], err = readJSON(file); err != nil {
			return 0, err
		}
	}
	diff := manifest.Diff(ms[0], ms[1])
	if len(diff) == 0 {
		return 0, nil
	}
	_, err := fmt.Fprintln(stdout, strings.Join(diff, "\n"))
	return 1, err
}

func readJSON(file string) (manifest.Manifest, error) {
	data, err := os.ReadFile(file) //nolint:gosec // G304: the manifest the caller asks to compare
	if err != nil {
		return manifest.Manifest{}, err
	}
	var m manifest.Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return manifest.Manifest{}, fmt.Errorf("%s: %w", file, err)
	}
	return m, nil
}

func printChanges(stdout io.Writer, app, published string, args []string) error {
	if app == "" {
		return usageError{"-changes needs -app"}
	}
	carriers := map[string]string{}
	for _, arg := range args {
		arch, archive, ok := strings.Cut(arg, "=")
		switch {
		case !ok || arch == "" || archive == "":
			return usageError{fmt.Sprintf("%q is not ARCH=ARCHIVE", arg)}
		case carriers[arch] != "":
			return usageError{"two carriers for " + arch}
		}
		carriers[arch] = archive
	}

	pairs := map[string]manifest.Pair{}
	for _, arch := range slices.Sorted(maps.Keys(carriers)) {
		candidate, err := read(carriers[arch], arch, app)
		if err != nil {
			return err
		}
		pair := manifest.Pair{Candidate: candidate}
		if published != "" {
			m, err := read(published, arch, app)
			switch {
			case err == nil:
				pair.Published = &m
			case !errors.Is(err, manifest.ErrNoImage):
				return err
			}
		}
		pairs[arch] = pair
	}
	text, err := manifest.Changes(pairs)
	if err != nil || text == "" {
		return err
	}
	_, err = fmt.Fprintln(stdout, text)
	return err
}

// complain writes to stderr after the command's name, one line per line of
// the message.
func complain(stderr io.Writer, format string, args ...any) {
	for line := range strings.Lines(fmt.Sprintf(format, args...)) {
		// A failed write to stderr leaves nowhere better to report it.
		_, _ = fmt.Fprintln(stderr, name+":", strings.TrimSuffix(line, "\n"))
	}
}

// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

// Package manifest reads what went into one of the carrier's FrankenPHP
// binaries from the records the build wrote beside it, and says what moved
// between two of them.
//
// A manifest is what the daily rebuild compares with the published carrier
// to decide whether to publish. spc resolves the Go toolchain to the newest
// release, xcaddy to its master branch's head whenever that Go is newer than
// the builder image's, several C libraries to their newest release, and
// libaom to its branch's head, while the build runs, so two builds of one
// commit can hold different code without a commit to show for it; nothing
// else in the image says so, and Trivy cannot see the libraries in the
// binary.
//
// It holds what decides the binary's code, and nothing that differs between
// two builds of the same inputs: FrankenPHP, PHP and Caddy as the binary
// reports them, the Go toolchain and every Go module it was built from, the
// xcaddy that wrote its main module and ran its build, the PHP modules it
// loads, and each C library spc built, as the version it resolved and the
// hash of the source it fetched. It leaves out the time spc fetched each
// source, the records' file times, the build's flags, which follow from
// the spc commit, the toolchain and xcaddy, and spc's resolver rules, which
// change only with the spc commit.
package manifest

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// SchemaVersion is the Schema every Manifest this package makes carries.
// Version 2 added Xcaddy.
const SchemaVersion = 2

// Manifest is what went into one app's binary for one architecture.
type Manifest struct {
	// Schema is SchemaVersion.
	Schema int `json:"schema"`
	// App is the app whose inputs built the binary, such as librespeed.
	App string `json:"app"`
	// Arch is the binary's architecture, as Go and OCI name it: amd64 or
	// arm64.
	Arch string `json:"arch"`
	// FrankenPHP, PHP and Caddy are the versions the binary reports.
	FrankenPHP string `json:"frankenphp"`
	PHP        string `json:"php"`
	Caddy      string `json:"caddy"`
	// Go is the toolchain that built it, such as go1.27.4.
	Go string `json:"go"`
	// Xcaddy is the version of the xcaddy module that wrote the binary's
	// main module and ran its build, as Go records it: a release, such as
	// v0.4.7, or a pseudo-version naming a commit of xcaddy's master
	// branch, the one spc installed or else the builder image's own.
	Xcaddy string `json:"xcaddy"`
	// Extensions are the PHP modules it loads, as PHP names them, sorted.
	Extensions []string `json:"extensions"`
	// Libraries maps each C library spc built, and any PHP extension it
	// fetched on its own, to the version spc resolved, or the file it
	// fetched when it names no version, then "@" and the hash of the
	// source: a SHA-1 for an archive, the commit for a git checkout.
	Libraries map[string]string `json:"libraries"`
	// GoModules maps each Go module in the binary to its version, and to
	// its replacement after " => " when it has one.
	GoModules map[string]string `json:"go_modules"`
}

// The records the build writes into the carrier's /records/, which
// records.sh describes.
const (
	recordApp           = "app.txt"
	recordVersion       = "version.txt"
	recordBuildInfo     = "build-info.txt"
	recordXcaddy        = "xcaddy.txt"
	recordExtensions    = "extensions.txt"
	recordSPCCache      = "spc-cache.json"
	recordBuildManifest = "build-manifest.json"
)

// FromRecords reads the manifest from root, a directory holding the
// records one build wrote.
func FromRecords(root *os.Root) (Manifest, error) {
	return parse(root.ReadFile)
}

// parse reads the manifest from the records read returns by name.
func parse(read func(name string) ([]byte, error)) (Manifest, error) {
	files := map[string][]byte{}
	var errs []error
	for _, name := range []string{recordApp, recordVersion, recordBuildInfo, recordXcaddy, recordExtensions, recordSPCCache, recordBuildManifest} {
		data, err := read(name)
		if err != nil {
			errs = append(errs, fmt.Errorf("read the record %s: %w", name, err))
		}
		files[name] = data
	}
	if err := errors.Join(errs...); err != nil {
		return Manifest{}, err
	}

	m := Manifest{Schema: SchemaVersion}
	var err error
	if m.App, err = parseApp(files[recordApp]); err != nil {
		errs = append(errs, err)
	}
	if m.FrankenPHP, m.PHP, m.Caddy, err = parseVersion(files[recordVersion]); err != nil {
		errs = append(errs, err)
	}
	if m.Go, m.Arch, m.GoModules, err = parseBuildInfo(files[recordBuildInfo]); err != nil {
		errs = append(errs, err)
	}
	if m.Xcaddy, err = parseXcaddy(files[recordXcaddy]); err != nil {
		errs = append(errs, err)
	}
	if m.Extensions, err = parseExtensions(files[recordExtensions]); err != nil {
		errs = append(errs, err)
	}
	if m.Libraries, err = parseLibraries(files[recordSPCCache], files[recordBuildManifest]); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func parseApp(data []byte) (string, error) {
	app := string(bytes.TrimSpace(data))
	if app == "" || strings.ContainsFunc(app, unicode.IsSpace) {
		return "", fmt.Errorf("%s: %q is not one app's name", recordApp, app)
	}
	return app, nil
}

// version.txt is `frankenphp version`, which ends with Caddy's module sum.
var versionLine = regexp.MustCompile(`^FrankenPHP (\S+) PHP (\S+) Caddy (\S+)(?: |$)`)

func parseVersion(data []byte) (frankenphp, php, caddy string, err error) {
	line, _, _ := strings.Cut(string(data), "\n")
	match := versionLine.FindStringSubmatch(line)
	if match == nil {
		return "", "", "", fmt.Errorf("%s: %q does not read FrankenPHP <version> PHP <version> Caddy <version>", recordVersion, line)
	}
	return match[1], match[2], match[3], nil
}

// parseBuildInfo reads `frankenphp build-info`, which is Go's
// debug.BuildInfo: tab-separated lines of go, path, mod, dep, => and build.
func parseBuildInfo(data []byte) (toolchain, arch string, modules map[string]string, err error) {
	modules = map[string]string{}
	last := ""
	for line := range strings.Lines(string(data)) {
		fields := strings.Split(strings.TrimRight(line, "\t\n"), "\t")
		switch fields[0] {
		case "go":
			if len(fields) > 1 {
				toolchain = fields[1]
			}
		case "dep":
			if len(fields) < 3 {
				return "", "", nil, fmt.Errorf("%s: %q is not dep <module> <version>", recordBuildInfo, strings.TrimSpace(line))
			}
			modules[fields[1]], last = fields[2], fields[1]
		case "=>":
			if last == "" || len(fields) < 3 {
				return "", "", nil, fmt.Errorf("%s: %q replaces no module", recordBuildInfo, strings.TrimSpace(line))
			}
			modules[last] += " => " + strings.Join(fields[1:3], " ")
			last = ""
		case "build":
			if len(fields) > 1 {
				if value, ok := strings.CutPrefix(fields[1], "GOARCH="); ok {
					arch = value
				}
			}
		default:
			last = ""
		}
	}
	var errs []error
	if toolchain == "" {
		errs = append(errs, fmt.Errorf("%s: no go line names the toolchain", recordBuildInfo))
	}
	if arch == "" {
		errs = append(errs, fmt.Errorf("%s: no build line names GOARCH", recordBuildInfo))
	}
	if len(modules) == 0 {
		errs = append(errs, fmt.Errorf("%s: no dep line names a Go module", recordBuildInfo))
	}
	if err := errors.Join(errs...); err != nil {
		return "", "", nil, err
	}
	return toolchain, arch, modules, nil
}

// xcaddyModule is the module xcaddy is built from.
const xcaddyModule = "github.com/caddyserver/xcaddy"

// parseXcaddy reads `go version -m` of spc's xcaddy: a line naming the
// file and its toolchain, then tab-indented lines of path, mod, dep and
// build. It returns the version on the one mod line, xcaddy's own.
func parseXcaddy(data []byte) (string, error) {
	var versions []string
	for line := range strings.Lines(string(data)) {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if fields[0] != "mod" {
			continue
		}
		if len(fields) < 3 || fields[1] != xcaddyModule {
			return "", fmt.Errorf("%s: %q is not mod %s <version>", recordXcaddy, strings.TrimSpace(line), xcaddyModule)
		}
		versions = append(versions, fields[2])
	}
	switch {
	case len(versions) != 1:
		return "", fmt.Errorf("%s: %d mod lines, not the one that names %s", recordXcaddy, len(versions), xcaddyModule)
	// What Go records for a build from a checkout: it names no commit.
	case versions[0] == "(devel)":
		return "", fmt.Errorf("%s: %s is (devel), which names no commit", recordXcaddy, xcaddyModule)
	}
	return versions[0], nil
}

func parseExtensions(data []byte) ([]string, error) {
	var extensions []string
	for line := range strings.Lines(string(data)) {
		if name := strings.TrimSpace(line); name != "" {
			extensions = append(extensions, name)
		}
	}
	if len(extensions) == 0 {
		return nil, fmt.Errorf("%s: lists no extension", recordExtensions)
	}
	slices.Sort(extensions)
	return extensions, nil
}

// spcSource is one source in spc's download cache. A null in the JSON
// leaves its field empty.
type spcSource struct {
	Version  string `json:"version"`
	Filename string `json:"filename"`
	Hash     string `json:"hash"`
}

type spcPackage struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Artifact string `json:"artifact"`
}

type spcBuildManifest struct {
	SchemaVersion int          `json:"schema-version"`
	Packages      []spcPackage `json:"packages"`
}

// parseLibraries reads every library spc built from its build manifest,
// and what it fetched for each from its download cache.
func parseLibraries(cacheData, manifestData []byte) (map[string]string, error) {
	var bm spcBuildManifest
	if err := json.Unmarshal(manifestData, &bm); err != nil {
		return nil, fmt.Errorf("%s: %w", recordBuildManifest, err)
	}
	// Another schema could name its libraries some other way, and reading
	// it as this one would find none and compare nothing.
	if bm.SchemaVersion != 1 {
		return nil, fmt.Errorf("%s: schema-version %d, not the 1 this reads", recordBuildManifest, bm.SchemaVersion)
	}
	var cache map[string]struct {
		Source *spcSource `json:"source"`
	}
	if err := json.Unmarshal(cacheData, &cache); err != nil {
		return nil, fmt.Errorf("%s: %w", recordSPCCache, err)
	}

	libraries := map[string]string{}
	var errs []error
	for _, p := range bm.Packages {
		// A PHP extension with an artifact of its own is fetched apart
		// from PHP's source, as a library is.
		if p.Type != "library" && (p.Type != "php-extension" || p.Artifact == "") {
			continue
		}
		entry, ok := cache[p.Artifact]
		switch {
		case !ok || entry.Source == nil:
			errs = append(errs, fmt.Errorf("%s: %s: no source for the artifact %q", recordSPCCache, p.Name, p.Artifact))
		case entry.Source.Hash == "" || entry.Source.Version == "" && entry.Source.Filename == "":
			errs = append(errs, fmt.Errorf("%s: %s: its source names no version or file, or no hash", recordSPCCache, p.Name))
		default:
			s := entry.Source
			libraries[p.Name] = cmp.Or(s.Version, s.Filename) + "@" + s.Hash
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if len(libraries) == 0 {
		return nil, fmt.Errorf("%s: names no library", recordBuildManifest)
	}
	return libraries, nil
}

// Diff returns what moved from a to b, one readable line per change, in
// the order the manifest's fields come, or nil when nothing did.
//
//nolint:gocritic // hugeParam: by value, so a caller sees Diff changes neither manifest
func Diff(a, b Manifest) []string {
	var lines []string
	for _, f := range []struct{ name, a, b string }{
		{"schema", strconv.Itoa(a.Schema), strconv.Itoa(b.Schema)},
		{"app", a.App, b.App},
		{"arch", a.Arch, b.Arch},
		{"frankenphp", a.FrankenPHP, b.FrankenPHP},
		{"php", a.PHP, b.PHP},
		{"caddy", a.Caddy, b.Caddy},
		{"go", a.Go, b.Go},
		{"xcaddy", a.Xcaddy, b.Xcaddy},
	} {
		if f.a != f.b {
			lines = append(lines, f.name+": "+f.a+" → "+f.b)
		}
	}
	for _, e := range union(a.Extensions, b.Extensions) {
		switch inA, inB := slices.Contains(a.Extensions, e), slices.Contains(b.Extensions, e); {
		case inB && !inA:
			lines = append(lines, "extension "+e+": added")
		case inA && !inB:
			lines = append(lines, "extension "+e+": removed")
		}
	}
	lines = append(lines, diffMap("library", a.Libraries, b.Libraries)...)
	lines = append(lines, diffMap("go module", a.GoModules, b.GoModules)...)
	return lines
}

// union returns every string in any of lists, once each, sorted.
func union(lists ...[]string) []string {
	all := slices.Concat(lists...)
	slices.Sort(all)
	return slices.Compact(all)
}

func diffMap(kind string, a, b map[string]string) []string {
	var lines []string
	for _, key := range union(slices.Collect(maps.Keys(a)), slices.Collect(maps.Keys(b))) {
		va, inA := a[key]
		vb, inB := b[key]
		switch {
		case !inA:
			lines = append(lines, kind+" "+key+": added at "+vb)
		case !inB:
			lines = append(lines, kind+" "+key+": removed, was "+va)
		case va != vb:
			lines = append(lines, kind+" "+key+": "+va+" → "+vb)
		}
	}
	return lines
}

// Pair is one architecture's candidate, the manifest of what a run built,
// and the manifest of what is published for it.
type Pair struct {
	Candidate Manifest
	// Published is nil when nothing is published for the architecture.
	Published *Manifest
}

// Arches are the architectures every carrier is built for. A publish
// pushes all of them from one run, or none.
var Arches = []string{"amd64", "arm64"}

// Changes says what moved between each architecture's published manifest
// and its candidate, as the content-change text the publish decision takes:
// each of Diff's lines after its architecture, such as "arm64: library
// libaom: …", one per line, or "<arch>: not published" for an architecture
// with nothing published. It returns "" when nothing moved.
//
// It refuses pairs that are not exactly one for each of Arches, or whose
// manifests are for another architecture than their key: a publish that
// compared only some architectures could push one that moved alongside one
// it never looked at.
func Changes(pairs map[string]Pair) (string, error) {
	var errs []error
	for _, arch := range slices.Sorted(maps.Keys(pairs)) {
		if !slices.Contains(Arches, arch) {
			errs = append(errs, fmt.Errorf("%s is not one of the architectures %s", arch, strings.Join(Arches, " and ")))
		}
	}
	var lines []string
	for _, arch := range Arches {
		p, ok := pairs[arch]
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("%s: no candidate to compare", arch))
			continue
		case p.Candidate.Arch != arch:
			errs = append(errs, fmt.Errorf("%s: the candidate is for %q", arch, p.Candidate.Arch))
			continue
		case p.Published == nil:
			lines = append(lines, arch+": not published")
			continue
		case p.Published.Arch != arch:
			errs = append(errs, fmt.Errorf("%s: the published manifest is for %q", arch, p.Published.Arch))
			continue
		}
		for _, line := range Diff(*p.Published, p.Candidate) {
			lines = append(lines, arch+": "+line)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

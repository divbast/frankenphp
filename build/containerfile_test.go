// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package build

import (
	"slices"
	"strings"
	"testing"
)

// TestContainerfilePkgRoot holds the Containerfile to the go-xcaddy package
// spc actually runs. spc unpacks it under PKG_ROOT_PATH, which the builder
// image sets, and runs `xcaddy build` from there; a path written out in full
// would keep pointing at the old place, and keep checking an xcaddy nothing
// ran, if the builder ever moved it.
func TestContainerfilePkgRoot(t *testing.T) {
	t.Parallel()

	containerfile := read(t, "../Containerfile")
	for i, line := range strings.Split(containerfile, "\n") {
		if strings.Contains(line, "/opt/spc-pkgroot") {
			t.Errorf("Containerfile:%d names spc's package root as a path, not as ${PKG_ROOT_PATH}: %s", i+1, strings.TrimSpace(line))
		}
	}
	for _, input := range []string{
		`GO_BIN="${PKG_ROOT_PATH}/go-xcaddy/bin/go"`,
		`XCADDY_BIN="${PKG_ROOT_PATH}/go-xcaddy/bin/xcaddy"`,
	} {
		// pinned-versions.sh and records.sh each read both.
		if n := strings.Count(containerfile, input); n != 2 {
			t.Errorf("Containerfile passes %s %d times, want 2: once to pinned-versions.sh, once to records.sh", input, n)
		}
	}
}

// TestContainerfileRecipeOnlyInRecords holds the recipe hash to the records
// stage. The build passes a new RECIPE_SHA256 on every edit to the
// Containerfile or the app's file, a comment among them, and a stage that
// declares an ARG puts its value in the cache key of every step after the
// declaration: declared in the build stage, any such edit would rebuild
// the binary from scratch, in a pull request's cached build too. In the
// records stage it reruns only records.sh.
func TestContainerfileRecipeOnlyInRecords(t *testing.T) {
	t.Parallel()

	stage := "(before any FROM)"
	var named []string
	for line := range strings.SplitSeq(read(t, "../Containerfile"), "\n") {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 0 || strings.HasPrefix(fields[0], "#"):
		case strings.EqualFold(fields[0], "FROM"):
			stage = "(unnamed)"
			if n := len(fields); n >= 4 && strings.EqualFold(fields[n-2], "AS") {
				stage = fields[n-1]
			}
		case strings.Contains(line, "RECIPE_SHA256"):
			named = append(named, stage+": "+strings.TrimSpace(line))
		}
	}
	if want := []string{"records: ARG RECIPE_SHA256"}; !slices.Equal(named, want) {
		t.Errorf("the Containerfile names RECIPE_SHA256 in %q, want only %q", named, want)
	}
}

// TestContainerfileExpectationsAfterBuild holds EXPECTED_EXTENSIONS and
// REQUIRED_GD_FEATURES to the line between the build and the check that
// reads them. They are inputs of extensions.sh alone: declared before the
// build RUN, an edit to either would give the binary's build a new cache
// key and rebuild it, to rerun a check. Declared in the stage again after
// the RUN, they reach extensions.sh, which a build test in checks_test.go
// cannot see, since it passes the environment itself.
func TestContainerfileExpectationsAfterBuild(t *testing.T) {
	t.Parallel()

	lines := strings.Split(read(t, "../Containerfile"), "\n")
	at := func(prefix string) int {
		t.Helper()
		found := -1
		for i, line := range lines {
			if strings.HasPrefix(line, prefix) {
				if found >= 0 {
					t.Fatalf("Containerfile has %q at lines %d and %d, want once", prefix, found+1, i+1)
				}
				found = i
			}
		}
		if found < 0 {
			t.Fatalf("Containerfile has no line starting %q", prefix)
		}
		return found
	}

	built := at("    /go/src/app/build-static.sh")
	checked := at("RUN FRANKENPHP_BIN=/go/src/app/dist/frankenphp /tmp/extensions.sh")
	for _, arg := range []string{"ARG EXPECTED_EXTENSIONS", "ARG REQUIRED_GD_FEATURES"} {
		if i := at(arg); i <= built || i >= checked {
			t.Errorf("Containerfile:%d: %s must come after the build (line %d) and before extensions.sh runs (line %d)", i+1, arg, built+1, checked+1)
		}
	}
	// Anything before extensions.sh that names them reads an empty value.
	for i, line := range lines[:checked] {
		if strings.HasPrefix(strings.TrimSpace(line), "#") || strings.HasPrefix(line, "ARG ") {
			continue
		}
		if strings.Contains(line, "EXPECTED_EXTENSIONS") || strings.Contains(line, "REQUIRED_GD_FEATURES") {
			t.Errorf("Containerfile:%d reads an input declared later: %s", i+1, strings.TrimSpace(line))
		}
	}
}

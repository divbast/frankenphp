// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// manifestGuard is the one condition under which the build workflow's
// publish job compares manifests: the daily rebuild of the commit already
// published. A push or a re-run comparing too would publish what its own
// build fetched over what a daily run had published since for the same
// commit, which can be older.
const manifestGuard = `if [ "${EVENT}" = schedule ] && [ "${published}" = "${GITHUB_SHA}" ]; then`

// TestWorkflowComparesOnlyForTheDailyRebuild reads the build workflow and
// checks that fetching the published image, running -changes and passing
// -content-change each happen once, directly inside manifestGuard.
func TestWorkflowComparesOnlyForTheDailyRebuild(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "build.yml"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")

	for _, marker := range []string{"oci-archive:/tmp/published.tar", "frankenphp-manifest -changes", "-content-change"} {
		var at []int
		for i, line := range lines {
			if strings.Contains(line, marker) {
				at = append(at, i)
			}
		}
		if len(at) != 1 {
			t.Errorf("build.yml mentions %q on %d lines, not one", marker, len(at))
			continue
		}
		if got := enclosingIf(lines, at[0]); got != manifestGuard {
			t.Errorf("build.yml:%d (%s) sits inside %q, not %q", at[0]+1, marker, got, manifestGuard)
		}
	}
}

// enclosingIf returns the innermost shell `if …; then` line that encloses
// lines[at], trimmed, or "" when none does before the run: block starts.
func enclosingIf(lines []string, at int) string {
	depth := 0
	for i := at - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		switch {
		case strings.HasPrefix(line, "run:"):
			return ""
		case line == "fi":
			depth++
		case strings.HasPrefix(line, "if ") && strings.HasSuffix(line, "then"):
			if depth == 0 {
				return line
			}
			depth--
		}
	}
	return ""
}

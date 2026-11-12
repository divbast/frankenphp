// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package main

import (
	"os"
	"path/filepath"
	"regexp"
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
	lines := readLines(t, ".github", "workflows", "build.yml")

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

// TestPublishJobInstallsOnlyWhatItRuns reads the build workflow's publish
// job, which holds packages: write, id-token: write and attestations:
// write, and the justfile's ci-checkout recipe, which the job runs. It
// checks that:
//
//   - the job runs jdx/mise-action once, with install_args naming each tool
//     as mise.toml's [tools] writes it;
//   - no line of the job runs `just setup`, whose `mise install` installs
//     every linter, or `mise install`;
//   - the job's own env: sets MISE_AUTO_INSTALL: "false", without which
//     `mise exec`, the shell of every justfile recipe, installs every
//     missing tool before it runs one, and no step sets it to anything else;
//   - ci-checkout depends on no other recipe and runs no `mise install` or
//     `just setup`.
//
// ci-tools, the job's other recipe, is divbast/ci's, and this does not
// read it.
func TestPublishJobInstallsOnlyWhatItRuns(t *testing.T) {
	t.Parallel()
	job := jobLines(readLines(t, ".github", "workflows", "build.yml"), "publish")
	if job == nil {
		t.Fatal("build.yml has no publish job")
	}
	tools := miseTools(t)

	var mise [][]string
	for _, step := range stepLines(job) {
		for _, line := range step {
			if strings.Contains(line, "uses: jdx/mise-action@") {
				mise = append(mise, step)
				break
			}
		}
	}
	if len(mise) != 1 {
		t.Errorf("the publish job runs jdx/mise-action in %d steps, not one", len(mise))
	}
	for _, step := range mise {
		args, ok := "", false
		for _, line := range step {
			if value, found := strings.CutPrefix(strings.TrimSpace(line), "install_args:"); found {
				args, ok = strings.Trim(strings.TrimSpace(value), `"'`), true
			}
		}
		if !ok || strings.TrimSpace(args) == "" {
			t.Error("the publish job runs jdx/mise-action without install_args, so it installs every tool in mise.toml")
			continue
		}
		for tool := range strings.FieldsSeq(args) {
			if !tools[tool] {
				t.Errorf("the publish job's install_args names %q, which is not a key of mise.toml's [tools]", tool)
			}
		}
	}

	// Every step inherits the job's env:, and a step may only repeat it.
	env := jobEnv(job)
	autoInstallOff := false
	stepOff := regexp.MustCompile(`^MISE_AUTO_INSTALL: ("false"|'false'|false)$`)
	for i, line := range job {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "#"):
		case installs.MatchString(trimmed):
			t.Errorf("the publish job runs %q, which installs every tool in mise.toml", trimmed)
		case env[i] && trimmed == `MISE_AUTO_INSTALL: "false"`:
			autoInstallOff = true
		case strings.Contains(trimmed, "MISE_AUTO_INSTALL") && (env[i] || !stepOff.MatchString(trimmed)):
			t.Errorf("the publish job sets %q, which lets `mise exec` install every tool in mise.toml", trimmed)
		}
	}
	if !autoInstallOff {
		t.Error(`the publish job's env: does not set MISE_AUTO_INSTALL: "false", so the first recipe it runs installs every tool in mise.toml`)
	}

	recipe := recipeLines(readLines(t, "justfile"), "ci-checkout")
	switch {
	case recipe == nil:
		t.Fatal("the justfile has no ci-checkout recipe")
	case strings.TrimSpace(recipe[0]) != "ci-checkout:":
		t.Errorf("the justfile's ci-checkout recipe runs other recipes first: %q", recipe[0])
	}
	for _, line := range recipe[1:] {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") && installs.MatchString(trimmed) {
			t.Errorf("the justfile's ci-checkout recipe runs %q, which installs every tool in mise.toml", trimmed)
		}
	}
}

// installs matches a command that installs every tool in mise.toml.
var installs = regexp.MustCompile(`\bjust\s+setup\b|\bmise\s+install\b`)

// jobEnv returns which lines of job, as jobLines returns it, are in the
// job's own env: block, the one beside its steps: and before it.
func jobEnv(job []string) map[int]bool {
	in := map[int]bool{}
	open := false
	for i, line := range job[1:] {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
		case indent(line) == 4 && trimmed == "steps:":
			return in
		case indent(line) == 4:
			open = trimmed == "env:"
		case open:
			in[i+1] = true
		}
	}
	return in
}

// recipeLines returns the justfile recipe called name: the line that
// declares it, then its body, up to the next line that is not indented, or
// nil when the justfile has no such recipe.
func recipeLines(justfile []string, name string) []string {
	for i, line := range justfile {
		if line != name+":" && !strings.HasPrefix(line, name+": ") && !strings.HasPrefix(line, name+" ") {
			continue
		}
		end := i + 1
		for ; end < len(justfile); end++ {
			if next := justfile[end]; next != "" && indent(next) == 0 && !strings.HasPrefix(next, "\t") {
				break
			}
		}
		return justfile[i:end]
	}
	return nil
}

// readLines reads the repository's file at the path elems name, as lines.
func readLines(t *testing.T, elems ...string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, elems...)...))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(data), "\n")
}

// jobLines returns the lines of the workflow job called name, from its key
// under jobs: up to the next line indented no deeper than that key, or nil
// when the workflow has no such job.
func jobLines(workflow []string, name string) []string {
	for i, line := range workflow {
		if line != "  "+name+":" {
			continue
		}
		end := i + 1
		for ; end < len(workflow); end++ {
			next := workflow[end]
			if strings.TrimSpace(next) != "" && indent(next) <= 2 {
				break
			}
		}
		return workflow[i:end]
	}
	return nil
}

// stepLines splits the steps: of job into each step's lines.
func stepLines(job []string) [][]string {
	var steps [][]string
	at := -1
	for _, line := range job {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "steps:":
			at = 0
		case at < 0:
		case strings.HasPrefix(trimmed, "- ") && (at == 0 || indent(line) == at):
			at = indent(line)
			steps = append(steps, []string{line})
		case len(steps) > 0:
			steps[len(steps)-1] = append(steps[len(steps)-1], line)
		}
	}
	return steps
}

func indent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// miseTools returns the keys of mise.toml's [tools] table, as written
// there, such as "core:go".
func miseTools(t *testing.T) map[string]bool {
	t.Helper()
	tools := map[string]bool{}
	in := false
	for _, line := range readLines(t, "mise.toml") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "["):
			in = trimmed == "[tools]"
		case !in || trimmed == "" || strings.HasPrefix(trimmed, "#"):
		default:
			key, _, ok := strings.Cut(trimmed, "=")
			if !ok {
				t.Fatalf("mise.toml: %q in [tools] is not key = version", trimmed)
			}
			tools[strings.Trim(strings.TrimSpace(key), `"`)] = true
		}
	}
	if len(tools) == 0 {
		t.Fatal("mise.toml names no tools")
	}
	return tools
}

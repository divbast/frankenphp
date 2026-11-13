// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// update rewrites testdata/publish-shell.txt from build.yml, for a change
// to the publish job's shell that someone has read and means.
var update = flag.Bool("update", false, "rewrite testdata/publish-shell.txt from build.yml's publish job")

// publishShellFile is the publish job's shell as last read and meant.
const publishShellFile = "testdata/publish-shell.txt"

// TestPublishShellPinned checks that every run: block of build.yml's
// publish job, which runs beside packages: write, id-token: write and
// attestations: write, is exactly what testdata/publish-shell.txt holds.
// A check that reads the shell for writes to :<app> can always be talked
// round by one written some other way, so the whole shell is pinned
// instead, and every change to it shows here as a diff to be read.
func TestPublishShellPinned(t *testing.T) {
	t.Parallel()
	got := publishShell(t)
	if *update {
		if err := os.WriteFile(publishShellFile, []byte(got), 0o644); err != nil { //nolint:gosec // G306: a testdata file, as readable as the rest of the repository
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(publishShellFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("build.yml's publish job runs other shell than %s. Every line of it runs beside the job's write scopes, so a change is meant only once someone has read it: read the diff (- %s, + build.yml), and if it is meant, run\n\n"+
			"\tgo test ./cmd/frankenphp-manifest/ -run '^TestPublishShellPinned$' -update\n\n"+
			"and commit %s with the change.\n\n%s",
			publishShellFile, publishShellFile, publishShellFile,
			lineDiff(strings.Split(string(want), "\n"), strings.Split(got, "\n")))
	}
}

// publishShell returns every run: block of build.yml's publish job, in
// order, each under a line naming its step.
func publishShell(t *testing.T) string {
	t.Helper()
	job := jobLines(readLines(t, ".github", "workflows", "build.yml"), "publish")
	if job == nil {
		t.Fatal("build.yml has no publish job")
	}
	var b strings.Builder
	for i, step := range stepLines(job) {
		if err := checkStep(step); err != nil {
			t.Fatalf("publish step %d (%s): %v. Write it as the other steps are written, or this cannot pin what it runs", i+1, strings.TrimSpace(step[0]), err)
		}
		script, ok := runBlock(step)
		if !ok {
			continue
		}
		name, ok := stepKey(step, "name")
		if !ok {
			name = "(no name)"
		}
		fmt.Fprintf(&b, "## %s\n%s\n", name, script)
	}
	return b.String()
}

// stepKeys are the keys a publish step may set. Any other, shell: or
// working-directory: among them, could change what a run: block does
// without a line of it changing.
var stepKeys = []string{"name", "id", "if", "uses", "with", "env", "run"}

var (
	// keyValue matches a step's own key and its value, the step's indent
	// taken off.
	keyValue = regexp.MustCompile(`^([a-z][a-z-]*):(?: (.*))?$`)
	// entry matches one with: or env: entry, on one line.
	entry = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*: \S`)
)

// checkStep returns an error unless step is written in the plain YAML
// runBlock and stepKey read, so that nothing a step runs escapes them:
// "- " and a key on its first line, each key once and from stepKeys, at
// one indent; name, id, if, uses and an inline run as plain one-line
// values; with and env as one-line entries one indent deeper; and a run
// block as "|" alone, its lines deeper still. A block indicator such as
// |- or >, a value on the next line, a flow mapping, a quoted key, an
// anchor, an alias or a tag is refused, not read past.
func checkStep(step []string) error {
	keys := indent(step[0]) + 2
	seen := map[string]bool{}
	block, nested := false, false
	for i, line := range step {
		trimmedLine := strings.TrimSpace(line)
		text := trimmedLine
		switch {
		case i == 0:
			text = strings.TrimPrefix(text, "- ")
		case trimmedLine == "", block && indent(line) > keys, strings.HasPrefix(trimmedLine, "#"):
			continue
		case nested && indent(line) == keys+2 && entry.MatchString(trimmedLine):
			continue
		case indent(line) != keys:
			return fmt.Errorf("%q is neither one of the step's keys nor a one-line entry of its with or env", trimmedLine)
		}
		m := keyValue.FindStringSubmatch(text)
		if m == nil {
			return fmt.Errorf("%q is not a plain key and value", text)
		}
		key, value := m[1], m[2]
		switch {
		case !slices.Contains(stepKeys, key):
			return fmt.Errorf("it sets %s:, which this does not read", key)
		case seen[key]:
			return fmt.Errorf("it sets %s: twice", key)
		}
		seen[key] = true
		block, nested = key == "run" && value == "|", key == "with" || key == "env"
		switch {
		case block, nested && value == "":
		case nested, value == "", strings.ContainsAny(value[:1], `|>&*!{["'`):
			return fmt.Errorf("its %s: is %q, a form this does not read", key, value)
		}
	}
	return nil
}

// runBlock returns the step's run: script as bash gets it, a line or a
// literal block with its indent taken off and a newline after each line,
// and whether the step has one.
func runBlock(step []string) (string, bool) {
	keys := indent(step[0]) + 2
	for i, line := range step {
		trimmedLine := strings.TrimSpace(line)
		if i == 0 {
			trimmedLine = strings.TrimPrefix(trimmedLine, "- ")
		} else if indent(line) != keys {
			continue
		}
		value, ok := strings.CutPrefix(trimmedLine, "run:")
		if !ok {
			continue
		}
		if value = strings.TrimSpace(value); value != "|" {
			return value + "\n", true
		}
		var block []string
		for _, next := range step[i+1:] {
			if strings.TrimSpace(next) != "" && indent(next) <= keys {
				break
			}
			block = append(block, next)
		}
		for len(block) > 0 && strings.TrimSpace(block[len(block)-1]) == "" {
			block = block[:len(block)-1]
		}
		var b strings.Builder
		for _, l := range block {
			b.WriteString(strings.TrimPrefix(l, strings.Repeat(" ", keys+2)) + "\n")
		}
		return b.String(), true
	}
	return "", false
}

// lineDiff returns the lines that differ between want and got, "-" for a
// line only want has and "+" for one only got has, with two lines of
// context around each change.
func lineDiff(want, got []string) string {
	n, m := len(want), len(got)
	common := make([][]int, n+1)
	for i := range common {
		common[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if want[i] == got[j] {
				common[i][j] = common[i+1][j+1] + 1
			} else {
				common[i][j] = max(common[i+1][j], common[i][j+1])
			}
		}
	}
	type edit struct {
		mark byte
		text string
	}
	var edits []edit
	for i, j := 0, 0; i < n || j < m; {
		switch {
		case i < n && j < m && want[i] == got[j]:
			edits = append(edits, edit{' ', want[i]})
			i, j = i+1, j+1
		case j < m && (i == n || common[i][j+1] >= common[i+1][j]):
			edits = append(edits, edit{'+', got[j]})
			j++
		default:
			edits = append(edits, edit{'-', want[i]})
			i++
		}
	}
	var b strings.Builder
	last := -1
	for k, e := range edits {
		near := e.mark != ' '
		for d := -2; d <= 2 && !near; d++ {
			near = k+d >= 0 && k+d < len(edits) && edits[k+d].mark != ' '
		}
		if !near {
			continue
		}
		if last >= 0 && k > last+1 {
			b.WriteString("  …\n")
		}
		fmt.Fprintf(&b, "%c %s\n", e.mark, e.text)
		last = k
	}
	return b.String()
}

// The flags apps verify the attestation with, divbast/ci's
// frankenphpref.Verify, which the verify step must pass gh.
var verifyFlags = []string{
	"--repo divbast/frankenphp",
	"--signer-workflow divbast/frankenphp/.github/workflows/build.yml@refs/heads/main",
	"--source-ref refs/heads/main",
}

// packageSettings is where the owner makes the package public, which no
// workflow can, and so the hint a failed anonymous fetch gives.
const packageSettings = "https://github.com/orgs/divbast/packages/container/frankenphp/settings"

// The index the stubs serve, and another that is not its digest's.
var (
	stagedBytes = []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`)
	otherBytes  = []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[],"annotations":{"x":"y"}}`)
)

// TestVerifyStepBehaviour runs the verify step's own script, as Actions
// runs it, against stand-ins for skopeo, gh and sleep, and checks that it
// passes only when the index it fetched anonymously hashes to the staged
// digest and gh verifies its attestation within three tries, ten seconds
// apart, and that a failed fetch sets lasting and points the owner at the
// package's settings.
func TestVerifyStepBehaviour(t *testing.T) {
	t.Parallel()
	script := publishStepScript(t, "verify")
	digest := digestOf(stagedBytes)
	ghCall := "gh attestation verify %s/staged-index.json " + strings.Join(verifyFlags, " ")

	for _, tc := range []struct {
		name     string
		served   []byte // nil: skopeo cannot fetch it
		ghFails  string // the gh stub's STUB_GH_FAILS
		pass     bool
		gh       int
		sleeps   int
		lasting  bool
		saysWhat string
	}{
		{"verifies the staged index", stagedBytes, "0", true, 1, 0, false, ""},
		{"waits for an attestation the API has yet to serve", stagedBytes, "2", true, 3, 2, false, ""},
		{"fails after three tries", stagedBytes, "all", false, 3, 2, false, "in three tries"},
		{"refuses bytes that are not the digest's", otherBytes, "0", false, 0, 0, false, "hash to"},
		{"refuses an index it cannot fetch anonymously", nil, "0", false, 0, 0, true, packageSettings},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{"DIGEST": digest, "GH_TOKEN": "stub", "STUB_GH_FAILS": tc.ghFails}
			if tc.served == nil {
				env["STUB_SKOPEO_FAIL"] = "1"
			} else {
				env["STUB_SKOPEO_BYTES"] = writeTemp(t, tc.served)
			}
			r := runStep(t, script, env)

			checkPass(t, r, tc.pass)
			want := []string{"skopeo inspect --raw --no-creds docker://ghcr.io/divbast/frankenphp@" + digest}
			for range tc.gh {
				want = append(want, fmt.Sprintf(ghCall, r.temp))
			}
			if got := r.callsTo("skopeo", "gh"); !slices.Equal(got, want) {
				t.Errorf("skopeo and gh were called\n\t%q\nnot\n\t%q", got, want)
			}
			if got := r.callsTo("sleep"); !slices.Equal(got, slices.Repeat([]string{"sleep 10"}, tc.sleeps)) {
				t.Errorf("the step slept %q, not %d times for 10 seconds", got, tc.sleeps)
			}
			if got := strings.Contains(r.outputs, "lasting=true\n"); got != tc.lasting {
				t.Errorf("the step set lasting: %t, not %t; GITHUB_OUTPUT holds %q", got, tc.lasting, r.outputs)
			}
			if !strings.Contains(r.output, tc.saysWhat) {
				t.Errorf("the step does not say %q:\n%s", tc.saysWhat, r.output)
			}
			if tc.served != nil {
				if got, err := os.ReadFile(filepath.Join(r.temp, "staged-index.json")); err != nil || !bytes.Equal(got, tc.served) {
					t.Errorf("RUNNER_TEMP/staged-index.json holds %q (%v), not the bytes skopeo served", got, err)
				}
			}
		})
	}
}

// TestDryRunStepBehaviour runs the step that checks the move keeps the
// attested digest, as Actions runs it, against a stand-in for docker, and
// checks that it passes only when buildx's dry run shows the staged
// index's own bytes, sets lasting when it shows another index, and fails
// without setting it when the dry run itself fails, which a re-run can
// mend.
func TestDryRunStepBehaviour(t *testing.T) {
	t.Parallel()
	script := publishStepScript(t, "keep")
	digest := digestOf(stagedBytes)

	for _, tc := range []struct {
		name     string
		shown    []byte // nil: the dry run fails
		pass     bool
		lasting  bool
		saysWhat string
	}{
		{"keeps the attested digest", stagedBytes, true, false, ""},
		{"refuses a move that would push another index", otherBytes, false, true, "would push an index"},
		{"fails when the dry run fails", nil, false, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{"DIGEST": digest}
			if tc.shown == nil {
				env["STUB_DOCKER_FAIL"] = "1"
			} else {
				env["STUB_DOCKER_BYTES"] = writeTemp(t, tc.shown)
			}
			r := runStep(t, script, env)

			checkPass(t, r, tc.pass)
			want := []string{"docker buildx imagetools create --dry-run -t ghcr.io/divbast/frankenphp:baikal ghcr.io/divbast/frankenphp@" + digest}
			if got := r.callsTo("docker"); !slices.Equal(got, want) {
				t.Errorf("docker was called %q, not %q", got, want)
			}
			if got := strings.Contains(r.outputs, "lasting=true\n"); got != tc.lasting {
				t.Errorf("the step set lasting: %t, not %t; GITHUB_OUTPUT holds %q", got, tc.lasting, r.outputs)
			}
			if !strings.Contains(r.output, tc.saysWhat) {
				t.Errorf("the step does not say %q:\n%s", tc.saysWhat, r.output)
			}
		})
	}
}

// publishStepScript returns the run: script of the publish step whose id
// is id, which must read everything it is given from its environment.
func publishStepScript(t *testing.T, id string) string {
	t.Helper()
	job := jobLines(readLines(t, ".github", "workflows", "build.yml"), "publish")
	at := slices.IndexFunc(stepLines(job), hasID(id))
	if at < 0 {
		t.Fatalf("build.yml's publish job has no step with id %s", id)
	}
	script, ok := runBlock(stepLines(job)[at])
	switch {
	case !ok:
		t.Fatalf("the publish step %s has no run: script", id)
	case strings.Contains(script, "${{"):
		t.Fatalf("the publish step %s's script holds an expression Actions fills in, so this cannot run it as Actions does", id)
	}
	return script
}

// systemPath is where the steps find every command the stubs leave alone:
// the system's own tools, GNU on the runner and BSD on macOS, so the
// steps are shown to run on both.
const systemPath = "/usr/bin:/bin:/usr/sbin:/sbin"

// stepResult is what one run of a step's script came to.
type stepResult struct {
	code    int
	output  string // stdout and stderr together
	outputs string // what the step wrote to GITHUB_OUTPUT
	calls   []string
	temp    string // the run's RUNNER_TEMP
}

// callsTo returns the stub calls to any of the commands named, in order.
func (r *stepResult) callsTo(commands ...string) []string {
	var out []string
	for _, call := range r.calls {
		name, _, _ := strings.Cut(call, " ")
		if slices.Contains(commands, name) {
			out = append(out, call)
		}
	}
	return out
}

// runStep runs script as Actions runs a bash step, with the job's IMAGE
// and APP, env, and the stubs in testdata/stubs ahead of systemPath, and
// nothing else of the host's environment.
func runStep(t *testing.T, script string, env map[string]string) *stepResult {
	t.Helper()
	dir := t.TempDir()
	temp := filepath.Join(dir, "runner-temp")
	if err := os.Mkdir(temp, 0o755); err != nil { //nolint:gosec // G301: a test's own temporary directory
		t.Fatal(err)
	}
	file := writeIn(t, dir, "step.sh", []byte(script))
	outputs := writeIn(t, dir, "github-output", nil)
	log := writeIn(t, dir, "stub.log", nil)

	cmd := exec.CommandContext(t.Context(), "/bin/bash", "--noprofile", "--norc", "-eo", "pipefail", file) //nolint:gosec // G204: bash, as Actions runs a step, on a script this test wrote
	cmd.Env = []string{
		"PATH=" + stubBin(t) + string(os.PathListSeparator) + systemPath,
		"HOME=" + dir,
		"TMPDIR=" + temp,
		"RUNNER_TEMP=" + temp,
		"GITHUB_OUTPUT=" + outputs,
		"STUB_LOG=" + log,
		"IMAGE=ghcr.io/divbast/frankenphp",
		"APP=baikal",
	}
	for name, value := range env {
		cmd.Env = append(cmd.Env, name+"="+value)
	}
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); !ok {
			t.Fatalf("bash: %v", err)
		}
	}
	return &stepResult{
		code:    cmd.ProcessState.ExitCode(),
		output:  out.String(),
		outputs: readFile(t, outputs),
		calls:   strings.FieldsFunc(readFile(t, log), func(r rune) bool { return r == '\n' }),
		temp:    temp,
	}
}

// checkPass fails t unless the step passed when pass, or failed when not.
func checkPass(t *testing.T, r *stepResult, pass bool) {
	t.Helper()
	if (r.code == 0) != pass {
		t.Fatalf("the step exited %d, which is not a %s:\n%s", r.code, map[bool]string{true: "pass", false: "failure"}[pass], r.output)
	}
}

// stubBin returns a directory holding each stub in testdata/stubs under
// the name of the command it stands in for.
func stubBin(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	for _, name := range []string{"docker", "gh", "skopeo", "sleep"} {
		stub, err := filepath.Abs(filepath.Join("testdata", "stubs", name+".sh"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(stub, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

// digestOf returns data's digest as an OCI descriptor writes it.
func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// writeTemp writes data to a new file in a temporary directory and
// returns its path.
func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	return writeIn(t, t.TempDir(), "data", data)
}

// writeIn writes data to the file called name in dir and returns its path.
func writeIn(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// readFile returns the file at path, which the test made.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // G304: a path this test made
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

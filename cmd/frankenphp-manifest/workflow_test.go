// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// contentGate is the one condition under which the build workflow's
// publish job compares manifests, and with them gates its publish: a push
// or the daily rebuild, once anything is published, and a push only once
// main has moved past the published commit. With main where it was
// published, a content change publishes whatever started the run, and a
// merge's run comparing there could publish what its own build fetched
// over what a daily run had published since for the same commit, which
// can be older. A manual run never compares, and always publishes.
const contentGate = `if [[ -n "${published}" && ( "${EVENT}" == schedule || ( "${EVENT}" == push && "${published}" != "${GITHUB_SHA}" ) ) ]]; then`

// TestContentGateOnPushAndSchedule reads the build workflow and checks
// that fetching the published image, running -changes, passing
// -content-change and passing -content-gated each happen once, directly
// inside contentGate, so the publish is never gated without the compare
// that gates it. It then runs the decide step's own script, as Actions
// runs it, against stand-ins for git, go, skopeo and divbast/ci's tools,
// for each event and each place the published commit can stand, and
// checks which of them compare and what reaches the decision.
func TestContentGateOnPushAndSchedule(t *testing.T) {
	t.Parallel()
	lines := readLines(t, ".github", "workflows", "build.yml")

	for _, marker := range []string{"oci-archive:/tmp/published.tar", "frankenphp-manifest -changes", "-content-change", "-content-gated"} {
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
		if got := enclosingIf(lines, at[0]); got != contentGate {
			t.Errorf("build.yml:%d (%s) sits inside %q, not %q", at[0]+1, marker, got, contentGate)
		}
	}

	script := publishStepScript(t, "decide")
	const (
		built  = "1111111111111111111111111111111111111111"
		before = "2222222222222222222222222222222222222222"
		change = "arm64: library libaom: v3.15.3 → v3.15.4"
	)
	for _, tc := range []struct {
		name, event, published string
		compares               bool
	}{
		{"a push with nothing published", "push", "", false},
		{"a push of the published commit", "push", built, false},
		{"a push past the published commit", "push", before, true},
		{"the daily run with nothing published", "schedule", "", false},
		{"the daily run of the published commit", "schedule", built, true},
		{"the daily run past the published commit", "schedule", before, true},
		{"a manual run of the published commit", "workflow_dispatch", built, false},
		{"a manual run past the published commit", "workflow_dispatch", before, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			decision := filepath.Join(t.TempDir(), "decision-args")
			env := map[string]string{
				"EVENT": tc.event, "GITHUB_SHA": built, "GH_TOKEN": "stub",
				"STUB_PUBLISHED": tc.published, "STUB_CHANGE": change,
				"STUB_SKOPEO_BYTES": writeTemp(t, nil), "STUB_DECISION_ARGS": decision,
			}
			r := runStepIn(t, script, env, ciTools)
			checkPass(t, r, true)

			var compared []string
			if tc.compares {
				compared = []string{
					"skopeo copy --all --quiet --src-no-creds docker://ghcr.io/divbast/frankenphp:baikal oci-archive:/tmp/published.tar",
					"go run ./cmd/frankenphp-manifest -changes -app baikal -published /tmp/published.tar amd64=/tmp/carriers/amd64/carrier.tar arm64=/tmp/carriers/arm64/carrier.tar",
				}
			}
			if got := r.callsTo("skopeo", "go"); !slices.Equal(got, compared) {
				t.Errorf("the step ran\n\t%q\nnot\n\t%q", got, compared)
			}
			want := []string{"-event", tc.event, "-built", built, "-published", tc.published}
			if tc.compares {
				want = append(want, "-content-change", change, "-content-gated")
			}
			args := strings.Split(readFile(t, decision), "\x00")
			if got := args[:len(args)-1]; !slices.Equal(got, want) {
				t.Errorf("divbast-publish-decision got\n\t%q\nnot\n\t%q", got, want)
			}
		})
	}
}

// ciTools lays out divbast/ci's tools in dir, where the decide step runs
// them from: each a stand-in, testdata/stubs/tool.sh.
func ciTools(t *testing.T, dir string) {
	t.Helper()
	bin := filepath.Join(dir, ".ci", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil { //nolint:gosec // G301: a test's own temporary directory
		t.Fatal(err)
	}
	stub, err := filepath.Abs(filepath.Join("testdata", "stubs", "tool.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"divbast-published-revision", "divbast-publish-decision"} {
		if err := os.Symlink(stub, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
}

// recipeLine is how the build hashes the recipe, the Containerfile and
// then the app's own file as one stream, and recipeArg how it passes
// sha256sum's hash, without the file name after it, to the records stage.
const (
	recipeLine = `recipe=$(cat Containerfile "apps/${APP}.env" | sha256sum)`
	recipeArg  = `--build-arg "RECIPE_SHA256=${recipe%% *}"`
)

// TestRecipeHashCoversBothFiles checks that the build workflow's build
// step hashes the recipe with recipeLine and passes it with recipeArg, on
// the one line of the step that names RECIPE_SHA256, and runs recipeLine
// for each app, checking that it gives the sha256 of the
// Containerfile followed by apps/<app>.env: an edit to either is then a
// content change.
func TestRecipeHashCoversBothFiles(t *testing.T) {
	t.Parallel()
	steps := stepLines(jobLines(readLines(t, ".github", "workflows", "build.yml"), "build"))
	at := slices.IndexFunc(steps, func(step []string) bool { return hasLine(step, "- name: Build the carrier") })
	if at < 0 {
		t.Fatal("build.yml's build job has no step named Build the carrier")
	}
	if !hasLine(steps[at], recipeLine) {
		t.Errorf("the build step does not hash the recipe as %s", recipeLine)
	}
	// Of two --build-arg RECIPE_SHA256, the last wins, so a second would
	// replace the hash without this line changing.
	var named []string
	for _, line := range steps[at] {
		if strings.Contains(line, "RECIPE_SHA256") {
			named = append(named, strings.TrimSpace(line))
		}
	}
	if len(named) != 1 || named[0] != recipeArg {
		t.Errorf("the build step names RECIPE_SHA256 on %q, not once, as %s", named, recipeArg)
	}

	root := filepath.Join("..", "..")
	containerfile := readFile(t, filepath.Join(root, "Containerfile"))
	seen := map[string]string{}
	for _, app := range []string{"baikal", "librespeed"} {
		cmd := exec.CommandContext(t.Context(), "/bin/bash", "-euo", "pipefail", "-c", recipeLine+`; printf '%s' "${recipe%% *}"`)
		cmd.Dir = root
		cmd.Env = []string{"PATH=" + systemPath, "APP=" + app}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s for %s: %v", recipeLine, app, err)
		}
		sum := sha256.Sum256([]byte(containerfile + readFile(t, filepath.Join(root, "apps", app+".env"))))
		if want := hex.EncodeToString(sum[:]); string(out) != want {
			t.Errorf("the recipe of %s hashes to %q, not %s, the sha256 of the Containerfile and apps/%s.env", app, out, want, app)
		}
		seen[string(out)] = app
	}
	if len(seen) != 2 {
		t.Errorf("both apps' recipes hash to one value, %q", slices.Collect(maps.Keys(seen)))
	}
}

// slotsProblems reports every way a job of the stall workflow that runs
// its slots check fails to pass content-gated: true and slot-job, the
// publish job's name in that app's slot run, "publish (<tag>)" for the
// image published as frankenphp:<tag> (FP-3, backlog). Without slot-job,
// stall reads the whole run's conclusion, and a leg that failed for
// another app fails this app's slot. It returns the number of slots jobs
// it found, to be told from none.
func slotsProblems(workflow []string) (problems []string, slots int) {
	for _, name := range mappingKeys(topLevel(workflow, "jobs"), 2) {
		job := jobLines(workflow, name)
		at := slices.IndexFunc(job, func(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), "uses: ") })
		if at < 0 {
			continue
		}
		checks, _ := stepWith(job, at, "checks")
		if !strings.Contains(checks, `"slots"`) {
			continue
		}
		slots++
		if gated, ok := stepWith(job, at, "content-gated"); !ok || gated != "true" {
			problems = append(problems, fmt.Sprintf("stall.yml's %s job runs the slots check with content-gated: %q, not true", name, gated))
		}
		published, _ := stepWith(job, at, "published")
		_, tag, _ := strings.Cut(published, "ghcr.io/divbast/frankenphp:")
		want := "publish (" + tag + ")"
		if tag == "" {
			problems = append(problems, fmt.Sprintf("stall.yml's %s job publishes %q, not ghcr.io/divbast/frankenphp:<tag>", name, published))
		}
		if got, _ := stepWith(job, at, "slot-job"); strings.Trim(got, `"'`) != want {
			problems = append(problems, fmt.Sprintf("stall.yml's %s job runs the slots check with slot-job: %q, not %q", name, got, want))
		}
	}
	return problems, slots
}

// TestStallSlotsContentGated checks that every job of the stall workflow
// that runs its slots check passes content-gated: true and its own
// publish job as slot-job, as slotsProblems reads them. The publish job
// gates each publish on its content, so a daily slot can succeed and leave
// an older commit published; without content-gated, stall counts each such
// slot as lost and fails two days after any merge that left the binary as
// it was. Against stall.yml as it is, then with each change below, which
// the check must refuse.
func TestStallSlotsContentGated(t *testing.T) {
	t.Parallel()
	text := strings.Join(readLines(t, ".github", "workflows", "stall.yml"), "\n")
	problems, slots := slotsProblems(strings.Split(text, "\n"))
	if slots != 2 {
		t.Fatalf("stall.yml has %d jobs that run the slots check, not 2", slots)
	}
	for _, problem := range problems {
		t.Error(problem)
	}
	for name, mutant := range map[string]string{
		"no slot-job for baikal":       strings.Replace(text, "      slot-job: publish (baikal)\n", "", 1),
		"no slot-job for librespeed":   strings.Replace(text, "      slot-job: publish (librespeed)\n", "", 1),
		"another app's slot-job":       strings.Replace(text, "slot-job: publish (baikal)", "slot-job: publish (librespeed)", 1),
		"the matrix job's raw name":    strings.Replace(text, "slot-job: publish (baikal)", "slot-job: publish (${{ matrix.app }})", 1),
		"content-gated off, baikal":    strings.Replace(text, "      content-gated: true\n", "", 1),
		"content-gated false, a slots": strings.Replace(text, "content-gated: true", "content-gated: false", 1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if mutant == text {
				t.Fatal("the change changed nothing")
			}
			if got, _ := slotsProblems(strings.Split(mutant, "\n")); len(got) == 0 {
				t.Error("passes stall.yml with " + name)
			}
		})
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
//     missing tool before it runs one, and nothing else in the job names it,
//     not even a step repeating that value;
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

	for _, problem := range autoInstallProblems(job) {
		t.Error(problem)
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

// autoInstallProblems reports every way the job fails to set
// MISE_AUTO_INSTALL: "false" once, in its own env:. Every step inherits
// that, so a line naming it anywhere else, a step's env: repeating the
// value included, is a second place to edit and a way for the two to part.
func autoInstallProblems(job []string) []string {
	var problems []string
	env := jobEnv(job)
	set := 0
	for i, line := range job {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "#"):
		case installs.MatchString(trimmed):
			problems = append(problems, fmt.Sprintf("the publish job runs %q, which installs every tool in mise.toml", trimmed))
		case env[i] && trimmed == `MISE_AUTO_INSTALL: "false"`:
			set++
		case strings.Contains(trimmed, "MISE_AUTO_INSTALL"):
			problems = append(problems, fmt.Sprintf("the publish job sets %q outside its own env:, or to something but \"false\"; the job's env: is the one place, and a step repeating it can part from it", trimmed))
		}
	}
	switch {
	case set == 0:
		problems = append(problems, `the publish job's env: does not set MISE_AUTO_INSTALL: "false", so the first recipe it runs installs every tool in mise.toml`)
	case set > 1:
		problems = append(problems, `the publish job's env: sets MISE_AUTO_INSTALL: "false" more than once`)
	}
	return problems
}

// TestAutoInstallProblems proves autoInstallProblems fails on each way the
// job can go wrong: no setting, a repeat in a step in any spelling of
// false, a different value, and a setting beside the job's.
func TestAutoInstallProblems(t *testing.T) {
	t.Parallel()
	const head = "  publish:\n    env:\n      IMAGE: x\n"
	const off = "      MISE_AUTO_INSTALL: \"false\"\n"
	const steps = "    steps:\n      - run: true\n"
	tests := []struct {
		name string
		job  string
		want string // a substring of the one problem, or "" for none
	}{
		{"once, in the job env", head + off + steps, ""},
		{"absent", head + steps, "does not set"},
		{"step repeats it", head + off + steps + "        env:\n          MISE_AUTO_INSTALL: \"false\"\n", "outside its own env"},
		{"step repeats it unquoted", head + off + steps + "        env:\n          MISE_AUTO_INSTALL: false\n", "outside its own env"},
		{"step turns it on", head + off + steps + "        env:\n          MISE_AUTO_INSTALL: \"true\"\n", "outside its own env"},
		{"job env twice", head + off + off + steps, "more than once"},
		{"job env true", head + "      MISE_AUTO_INSTALL: \"true\"\n" + steps, "outside its own env"},
		{"a commented mention is fine", head + off + "      # MISE_AUTO_INSTALL: \"false\"\n" + steps, ""},
		{"mise install", head + off + steps + "      - run: mise install\n", "installs every tool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			problems := autoInstallProblems(strings.Split(strings.TrimSuffix(tt.job, "\n"), "\n"))
			switch {
			case tt.want == "" && len(problems) != 0:
				t.Errorf("problems = %q, want none", problems)
			case tt.want != "" && (len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), tt.want)):
				t.Errorf("problems = %q, want one containing %q", problems, tt.want)
			}
		})
	}
}

// installs matches a command that installs every tool in mise.toml.
var installs = regexp.MustCompile(`\bjust\s+setup\b|\bmise\s+install\b`)

// The publish job's needs: and if:. Each app's job waits for lint and for
// the whole build matrix, and still runs when a leg of it failed, so one
// app's broken build holds back only its own publish: that app's job then
// fails fetching the carrier its leg never uploaded, before it pushes
// anything (TestPublishDownloadsBeforePush). Pull requests still need
// every job, through required.
const (
	publishNeeds = "[lint, build]"
	publishIf    = "${{ !cancelled() && needs.lint.result == 'success' && github.ref == 'refs/heads/main' && (github.event_name == 'push' || github.event_name == 'schedule' || github.event_name == 'workflow_dispatch') }}"
)

// TestPublishGatedPerApp checks that the publish job's needs: and if: are
// publishNeeds and publishIf, exactly, and that required, the one check
// main's ruleset requires, still needs lint and every build leg.
func TestPublishGatedPerApp(t *testing.T) {
	t.Parallel()
	workflow := readLines(t, ".github", "workflows", "build.yml")
	for _, tc := range []struct{ job, key, want string }{
		{"publish", "needs", publishNeeds},
		{"publish", "if", publishIf},
		{"required", "needs", "[lint, build]"},
		{"required", "if", "always()"},
	} {
		t.Run(tc.job+" "+tc.key, func(t *testing.T) {
			t.Parallel()
			job := jobLines(workflow, tc.job)
			if job == nil {
				t.Fatalf("build.yml has no %s job", tc.job)
			}
			got, ok := jobKey(job, tc.key)
			switch {
			case !ok:
				t.Errorf("the %s job sets no %s:", tc.job, tc.key)
			case got != tc.want:
				t.Errorf("the %s job's %s: is %q, not %q", tc.job, tc.key, got, tc.want)
			}
		})
	}
}

// TestPublishDownloadsBeforePush checks what keeps a broken app's publish
// from pushing anything once publishIf lets it run: the publish job
// fetches both of its app's carriers, with no if: or continue-on-error:
// that could pass over a missing one, before its first skopeo copy; and
// each build leg uploads its carrier as its last step, so a leg that
// failed at any step uploads none.
func TestPublishDownloadsBeforePush(t *testing.T) {
	t.Parallel()
	workflow := readLines(t, ".github", "workflows", "build.yml")
	publish := stepLines(jobLines(workflow, "publish"))
	firstCopy := slices.IndexFunc(publish, func(step []string) bool { return mentions(step, "skopeo copy") })
	if firstCopy < 0 {
		t.Fatal("the publish job runs no skopeo copy")
	}
	for _, arch := range []string{"amd64", "arm64"} {
		name := "name: carrier-${{ matrix.app }}-" + arch
		at := slices.IndexFunc(publish, func(step []string) bool {
			return mentions(step, "uses: actions/download-artifact@") && hasLine(step, name)
		})
		switch {
		case at < 0:
			t.Errorf("the publish job never downloads %s", name)
			continue
		case at > firstCopy:
			t.Errorf("the publish job downloads %s after its first skopeo copy", name)
		}
		for _, line := range trimmed(publish[at]) {
			if strings.HasPrefix(line, "if:") || strings.HasPrefix(line, "continue-on-error:") {
				t.Errorf("the publish job's download of %s sets %q, so a missing carrier need not fail it", name, line)
			}
		}
	}

	build := stepLines(jobLines(workflow, "build"))
	if len(build) == 0 {
		t.Fatal("build.yml's build job has no steps")
	}
	last := build[len(build)-1]
	if !mentions(last, "uses: actions/upload-artifact@") {
		t.Errorf("the build job's last step is not its upload-artifact step: %q", trimmed(last))
	}
	for _, want := range []string{"name: carrier-${{ matrix.app }}-${{ matrix.arch }}", "if-no-files-found: error"} {
		if !hasLine(last, want) {
			t.Errorf("the build job's last step has no line %q", want)
		}
	}
	for _, step := range build {
		for _, line := range trimmed(step) {
			if strings.HasPrefix(line, "continue-on-error:") {
				t.Errorf("a build step sets %q, so a leg could upload a carrier after a failed step", line)
			}
		}
	}
}

// publishOnly is the if: of every publish step from the first push to the
// move: each runs only when the decision says publish, and no failure
// before it lets it run.
const publishOnly = "steps.decide.outputs.publish == 'true'"

// TestPublishAttestsBeforePromote checks the shape of the publish job
// around the move of :<app>, which testdata/publish-shell.txt pins line
// for line (TestPublishShellPinned) and publish_test.go runs:
//
//   - the job pushes each architecture, stages the index, attests the
//     staged digest, verifies it, dry-runs the move, moves :<app> and
//     checks the tag anonymously, in that order, each of the steps after
//     the stage reading the staged digest;
//   - every step from the push to the move runs only on publishOnly, and
//     no step of the job sets continue-on-error:;
//   - a last step, run only on a failure that came before the move, says
//     what it left, and leaves out the re-run advice when the failing
//     step set lasting;
//   - the build legs keep their carriers for the one day that note
//     promises.
func TestPublishAttestsBeforePromote(t *testing.T) {
	t.Parallel()
	workflow := readLines(t, ".github", "workflows", "build.yml")
	job := jobLines(workflow, "publish")
	if job == nil {
		t.Fatal("build.yml has no publish job")
	}
	steps := stepLines(job)
	const fromStage = "DIGEST: ${{ steps.stage.outputs.digest }}"

	const (
		push = iota
		stage
		attest
		verify
		keep
		promote
		check
		note
	)
	order := []struct {
		what string
		find func([]string) bool
		want []string
	}{
		push: {
			"pushes each architecture",
			func(s []string) bool {
				return mentions(s, "skopeo copy") && mentions(s, `"docker://${IMAGE}:${APP}-${arch}"`)
			},
			nil,
		},
		stage: {"stages the index", hasID("stage"), nil},
		attest: {
			"attests the staged index",
			func(s []string) bool { return mentions(s, "uses: actions/attest@") },
			[]string{"subject-name: ghcr.io/divbast/frankenphp", "subject-digest: ${{ steps.stage.outputs.digest }}", "push-to-registry: true"},
		},
		verify:  {"verifies the attestation", hasID("verify"), []string{fromStage}},
		keep:    {"checks the move keeps the attested digest", hasID("keep"), []string{fromStage}},
		promote: {"moves the tag", hasID("promote"), []string{fromStage}},
		check: {
			"checks the tag anonymously",
			func(s []string) bool {
				return mentions(s, `skopeo inspect --raw --no-creds "docker://${IMAGE}:${APP}"`)
			},
			[]string{fromStage},
		},
		note: {
			"says what a failure before the move left",
			func(s []string) bool { return mentions(s, "steps.promote.outcome") },
			[]string{
				"if: failure() && " + publishOnly + " && steps.promote.outcome == 'skipped'",
				"LASTING: ${{ steps.verify.outputs.lasting }}${{ steps.keep.outputs.lasting }}",
			},
		},
	}
	at := make([]int, 0, len(order))
	before := ""
	for _, o := range order {
		var found []int
		for j, step := range steps {
			if o.find(step) {
				found = append(found, j)
			}
		}
		if len(found) != 1 {
			t.Fatalf("the publish job has %d steps where it %s, not one", len(found), o.what)
		}
		for _, want := range o.want {
			if !hasLine(steps[found[0]], want) {
				t.Errorf("the step that %s has no line %q", o.what, want)
			}
		}
		if len(at) > 0 && found[0] <= at[len(at)-1] {
			t.Errorf("the step that %s runs before the one that %s", o.what, before)
		}
		at, before = append(at, found[0]), o.what
	}
	if at[note] != len(steps)-1 {
		t.Error("the step that says what a failure left is not the publish job's last")
	}

	// A failure before the move must stop it: no step skips the decision's
	// guard or lets a failure pass.
	for i := at[push]; i <= at[promote]; i++ {
		if got, _ := stepKey(steps[i], "if"); got != publishOnly {
			t.Errorf("publish step %d (%s) runs on if: %q, not %q", i+1, strings.TrimSpace(steps[i][0]), got, publishOnly)
		}
	}
	for _, line := range trimmed(job) {
		if strings.HasPrefix(line, "continue-on-error:") {
			t.Errorf("the publish job sets %q, so a failed step can let :<app> move", line)
		}
	}

	// The note's day is how long the build legs keep the carriers.
	build := stepLines(jobLines(workflow, "build"))
	if len(build) == 0 || !hasLine(build[len(build)-1], "retention-days: 1") {
		t.Error("the build legs keep their carriers for other than one day, which the failure note says")
	}
}

// publishPermissions is all the publish job holds, as it held before it
// attested before promoting: a job with write scopes gets nothing more.
var publishPermissions = map[string]string{
	"contents":     "read",
	"packages":     "write",
	"id-token":     "write",
	"attestations": "write",
}

// TestPublishPermissionsUnchanged checks that the publish job's
// permissions: are exactly publishPermissions.
func TestPublishPermissionsUnchanged(t *testing.T) {
	t.Parallel()
	job := jobLines(readLines(t, ".github", "workflows", "build.yml"), "publish")
	if job == nil {
		t.Fatal("build.yml has no publish job")
	}
	if got := jobMap(job, "permissions"); !maps.Equal(got, publishPermissions) {
		t.Errorf("the publish job's permissions are %v, not %v", got, publishPermissions)
	}
}

// The shared Renovate managers' patterns for the two build tools the
// workflow pins (divbast/renovate-config's default.json, customManagers),
// as Renovate reads them: a pin in any other shape is one Renovate never
// moves.
//
// These are copies. The preset's default.json is the source, and
// renovate-config's CLAUDE.md lists every copy: a change to a pattern
// there is a change to buildkitPin and misePin here (FP-4, backlog).
var (
	buildkitPin = regexp.MustCompile(`image=(?<depName>moby/buildkit):(?<currentValue>v[0-9.]+)@(?<currentDigest>sha256:[a-f0-9]{64})`)
	misePin     = regexp.MustCompile(`# renovate: datasource=(?<datasource>github-releases) depName=(?<depName>jdx/mise)\n\s*MISE_VERSION: ["']?(?<currentValue>[0-9.]+)["']?`)
)

// TestBuildToolsPinned checks that the BuildKit daemon and mise, which
// the actions would otherwise take at whatever release is newest, are
// pinned where Renovate's shared managers read them:
//
//   - every docker/setup-buildx-action step sets driver-opts to one
//     moby/buildkit version and digest, buildkitPin's shape, the same in
//     every step;
//   - the workflow's own env: holds MISE_VERSION under its renovate
//     marker, misePin's shape, and every jdx/mise-action step passes it
//     as its version.
func TestBuildToolsPinned(t *testing.T) {
	t.Parallel()
	workflow := readLines(t, ".github", "workflows", "build.yml")

	var buildx, mise, pins []string
	for i, line := range workflow {
		trimmedLine := strings.TrimSpace(line)
		switch {
		case strings.Contains(trimmedLine, "uses: docker/setup-buildx-action@"):
			buildx = append(buildx, fmt.Sprintf("build.yml:%d", i+1))
			opts, ok := stepWith(workflow, i, "driver-opts")
			m := buildkitPin.FindStringSubmatch(opts)
			if !ok || m == nil || opts != m[0] {
				t.Errorf("build.yml:%d: setup-buildx-action's driver-opts is %q, not image=moby/buildkit:v<version>@sha256:<digest>", i+1, opts)
				continue
			}
			pins = append(pins, opts)
		case strings.Contains(trimmedLine, "uses: jdx/mise-action@"):
			mise = append(mise, fmt.Sprintf("build.yml:%d", i+1))
			if version, ok := stepWith(workflow, i, "version"); !ok || version != "${{ env.MISE_VERSION }}" {
				t.Errorf("build.yml:%d: mise-action's version is %q, not ${{ env.MISE_VERSION }}", i+1, version)
			}
		}
	}
	if len(buildx) == 0 || len(mise) == 0 {
		t.Fatalf("build.yml runs setup-buildx-action at %q and mise-action at %q; with none of either, this checks nothing of it", buildx, mise)
	}
	if distinct := slices.Compact(slices.Sorted(slices.Values(pins))); len(distinct) > 1 {
		t.Errorf("build.yml pins BuildKit %d ways: %q", len(distinct), distinct)
	}

	env := strings.Join(topLevel(workflow, "env"), "\n")
	if m := misePin.FindStringSubmatch(env); m == nil {
		t.Errorf("build.yml's own env: holds no MISE_VERSION under %q: %q", "# renovate: datasource=github-releases depName=jdx/mise", env)
	}
}

// jobKey returns the value of the job's own key, one indent below the
// job's name and before its steps:, trimmed, and whether the job sets it.
func jobKey(job []string, key string) (string, bool) {
	for _, line := range job[1:] {
		if indent(line) != 4 {
			continue
		}
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "steps:" {
			break
		}
		if value, ok := strings.CutPrefix(trimmedLine, key+":"); ok {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

// jobMap returns the entries of the job's own key, a mapping one indent
// below the job's name, each value without its comment, or the key's
// value under its own name when it is written on one line.
func jobMap(job []string, key string) map[string]string {
	out := map[string]string{}
	open := false
	for _, line := range job[1:] {
		trimmedLine := strings.TrimSpace(line)
		switch {
		case trimmedLine == "" || strings.HasPrefix(trimmedLine, "#"):
		case indent(line) <= 4 && open:
			return out
		case indent(line) == 4:
			if value, ok := strings.CutPrefix(trimmedLine, key+":"); ok {
				if value = strings.TrimSpace(value); value != "" {
					out[key] = value
					return out
				}
				open = true
			}
		case open:
			k, v, _ := strings.Cut(trimmedLine, ":")
			v, _, _ = strings.Cut(v, " #")
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

// stepKey returns the value of the step's own key, trimmed, and whether
// the step sets it.
func stepKey(step []string, key string) (string, bool) {
	keys := indent(step[0]) + 2
	for i, line := range step {
		trimmedLine := strings.TrimSpace(line)
		switch {
		case i == 0:
			trimmedLine = strings.TrimPrefix(trimmedLine, "- ")
		case indent(line) != keys:
			continue
		}
		if value, ok := strings.CutPrefix(trimmedLine, key+":"); ok {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

// stepWith returns the value of key in the with: of the step whose uses:
// is workflow[at], trimmed, and whether the step sets it there.
func stepWith(workflow []string, at int, key string) (string, bool) {
	keys := indent(workflow[at])
	if strings.HasPrefix(strings.TrimSpace(workflow[at]), "- ") {
		keys += 2
	}
	in := false
	for _, line := range workflow[at+1:] {
		trimmedLine := strings.TrimSpace(line)
		switch {
		case trimmedLine == "" || strings.HasPrefix(trimmedLine, "#"):
		case indent(line) < keys:
			return "", false
		case indent(line) == keys:
			in = trimmedLine == "with:"
		case in && indent(line) == keys+2:
			if value, ok := strings.CutPrefix(trimmedLine, key+":"); ok {
				return strings.TrimSpace(value), true
			}
		}
	}
	return "", false
}

// topLevel returns the lines of the workflow's top-level key called name,
// below its name up to the next line that is not indented.
func topLevel(workflow []string, name string) []string {
	for i, line := range workflow {
		if line != name+":" {
			continue
		}
		end := i + 1
		for ; end < len(workflow); end++ {
			if next := workflow[end]; next != "" && indent(next) == 0 {
				break
			}
		}
		return workflow[i+1 : end]
	}
	return nil
}

// hasID returns whether a step's id: is id.
func hasID(id string) func([]string) bool {
	return func(step []string) bool { return slices.Contains(trimmed(step), "id: "+id) }
}

// hasLine returns whether a line of step is want once trimmed, or want
// continued onto the next line with a backslash.
func hasLine(step []string, want string) bool {
	return slices.ContainsFunc(step, func(line string) bool {
		line = strings.TrimSpace(line)
		return line == want || line == want+` \`
	})
}

// mentions returns whether any line of step contains s.
func mentions(step []string, s string) bool {
	return slices.ContainsFunc(step, func(line string) bool { return strings.Contains(line, s) })
}

// trimmed returns lines with each line's blanks trimmed.
func trimmed(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimSpace(line)
	}
	return out
}

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

// miseIsolationProblems reports every way the job lets what mise makes of
// the repository's mise.toml reach its steps (FP-1, backlog; the rule is
// divbast/ci's CI-12, so it holds the same in both repositories):
//
//   - each jdx/mise-action step sets env, export_path and add_shims_to_path
//     to "false". All three default to true, under which the action writes
//     mise.toml's [env], the PATH entries mise makes and its shims'
//     directory into every later step, where a BASH_ENV or a PATH would
//     change what the pinned scripts run without a line of them changing;
//   - go and just run only through `mise exec --`, which gives that one
//     command the toolchain and the [env] of mise.toml, and nothing else.
func miseIsolationProblems(job []string) []string {
	var problems []string
	for i, line := range job {
		trimmedLine := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmedLine, "#"):
		case strings.Contains(trimmedLine, "uses: jdx/mise-action@"):
			for _, input := range []string{"env", "export_path", "add_shims_to_path"} {
				got, _ := stepWith(job, i, input)
				if strings.Trim(got, `"'`) != "false" {
					problems = append(problems, fmt.Sprintf("the publish job's jdx/mise-action sets %s: %q, not \"false\", so what mise makes of mise.toml reaches every later step", input, got))
				}
			}
		case !allThroughMise(trimmedLine):
			problems = append(problems, fmt.Sprintf("the publish job runs %q, not through mise exec --, so it needs mise's PATH", trimmedLine))
		}
	}
	return problems
}

// bareTool matches go or just as a command word: after the start of a line
// or a step's run:, a space, an opening parenthesis, a pipe or a semicolon.
var bareTool = regexp.MustCompile(`(?:^|[\s(|;&])(?:go|just)\s+[a-z./-]`)

// allThroughMise returns whether every go or just command on the line is
// preceded by mise exec --.
func allThroughMise(line string) bool {
	for _, at := range bareTool.FindAllStringIndex(line, -1) {
		if !strings.HasSuffix(strings.TrimRight(line[:at[0]], " "), "mise exec --") {
			return false
		}
	}
	return true
}

// TestPublishMiseIsolated checks miseIsolationProblems against the publish
// job as it is, then with each way of leaking mise.toml's [env] or PATH into
// the job, which it must refuse (FP-1).
func TestPublishMiseIsolated(t *testing.T) {
	t.Parallel()
	job := jobLines(readLines(t, ".github", "workflows", "build.yml"), "publish")
	if job == nil {
		t.Fatal("build.yml has no publish job")
	}
	if got := miseIsolationProblems(job); len(got) != 0 {
		t.Errorf("the publish job: %q", got)
	}
	text := strings.Join(job, "\n")
	for name, mutant := range map[string]string{
		"env left at its default":         strings.Replace(text, "          env: \"false\"\n", "", 1),
		"env on":                          strings.Replace(text, "          env: \"false\"\n", "          env: \"true\"\n", 1),
		"export_path left at its default": strings.Replace(text, "          export_path: \"false\"\n", "", 1),
		"export_path on":                  strings.Replace(text, "          export_path: \"false\"\n", "          export_path: true\n", 1),
		"shims left on PATH":              strings.Replace(text, "          add_shims_to_path: \"false\"\n", "", 1),
		"shims on":                        strings.Replace(text, "          add_shims_to_path: \"false\"\n", "          add_shims_to_path: true\n", 1),
		"just off mise's PATH":            strings.Replace(text, "mise exec -- just ci-checkout", "just ci-checkout", 1),
		"go off mise's PATH":              strings.Replace(text, "mise exec -- go run", "go run", 1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if mutant == text {
				t.Fatal("the change changed nothing")
			}
			if len(miseIsolationProblems(strings.Split(mutant, "\n"))) == 0 {
				t.Error("passes the publish job with " + name)
			}
		})
	}
}

// publishActions is every action the publish job uses, in the order its
// steps use them. The job holds packages: write, id-token: write and
// attestations: write, so an action it gains is a change to review here,
// not one that arrives with a line no golden pins (FP-5, backlog; the rule
// is divbast/ci's CI-11).
var publishActions = []string{
	"actions/checkout",
	"jdx/mise-action",
	"docker/setup-buildx-action",
	"actions/download-artifact",
	"actions/download-artifact",
	"docker/login-action",
	"actions/attest",
}

// actionPin is a uses: value as the workflows pin an action: owner/repo at
// a full commit SHA, with the version as a comment. Renovate and pinact
// move the commit and the version, so this fixes neither.
var actionPin = regexp.MustCompile(`^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)@[0-9a-f]{40} # v[0-9][0-9A-Za-z.+-]*$`)

// usesProblems reports every way the job's uses: steps differ from
// publishActions: another action, one missing, another order, or one not
// pinned as actionPin reads it.
func usesProblems(job []string) []string {
	var problems []string
	var used []string
	for _, step := range stepLines(job) {
		value, ok := stepKey(step, "uses")
		if !ok {
			continue
		}
		if q := value[:min(1, len(value))]; q == `"` || q == `'` {
			quoted, comment, _ := strings.Cut(value[1:], q)
			value = quoted + comment
		}
		m := actionPin.FindStringSubmatch(value)
		if m == nil {
			problems = append(problems, fmt.Sprintf("the publish job uses %q, not an action pinned to a full commit SHA with its version as a comment", value))
			continue
		}
		used = append(used, m[1])
	}
	if !slices.Equal(used, publishActions) {
		problems = append(problems, fmt.Sprintf("the publish job uses %q, want exactly %q, in that order", used, publishActions))
	}
	return problems
}

// TestPublishUsesOnlyItsActions checks usesProblems against the publish
// job as it is, then with each change below, which it must refuse, and
// with a pin moved as Renovate moves one, which it must accept (FP-5).
func TestPublishUsesOnlyItsActions(t *testing.T) {
	t.Parallel()
	job := jobLines(readLines(t, ".github", "workflows", "build.yml"), "publish")
	if job == nil {
		t.Fatal("build.yml has no publish job")
	}
	if got := usesProblems(job); len(got) != 0 {
		t.Errorf("the publish job: %q", got)
	}
	text := strings.Join(job, "\n")
	const (
		attest = "      - uses: actions/attest@"
		cache  = "      - uses: actions/cache@0057852bfaa89a56745cba8c7296529d2fc39830 # v4.3.0\n        with:\n          path: /tmp/x\n          key: x\n"
	)
	for name, mutant := range map[string]string{
		"one more action":          strings.Replace(text, attest, cache+attest, 1),
		"an action renamed":        strings.Replace(text, "uses: docker/login-action@", "uses: docker/login-actions@", 1),
		"an action by tag":         regexp.MustCompile(`actions/attest@[0-9a-f]{40} # (v[0-9.]+)`).ReplaceAllString(text, "actions/attest@$1"),
		"an action by a short SHA": regexp.MustCompile(`(actions/attest@[0-9a-f]{12})[0-9a-f]{28}`).ReplaceAllString(text, "$1"),
		"a pin with no version":    regexp.MustCompile(`(actions/attest@[0-9a-f]{40}) # v[0-9.]+`).ReplaceAllString(text, "$1"),
		"a local action":           strings.Replace(text, attest, "      - uses: ./.ci/.github/actions/sign\n"+attest, 1),
		"a container action":       strings.Replace(text, attest, "      - uses: docker://alpine:3\n"+attest, 1),
		"an action left out":       regexp.MustCompile(`(?m)^ {6}- uses: docker/login-action@.*\n(?: {8}.*\n)*`).ReplaceAllString(text, ""),
		"an action, mixed case":    strings.Replace(text, "uses: actions/download-artifact@", "uses: Actions/Download-Artifact@", 1),
		"two actions swapped":      swapLines(text, "      - uses: docker/setup-buildx-action@", "      - uses: jdx/mise-action@"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if mutant == text {
				t.Fatal("the change changed nothing")
			}
			if len(usesProblems(strings.Split(mutant, "\n"))) == 0 {
				t.Error("passes the publish job with " + name)
			}
		})
	}
	t.Run("a pin Renovate moves", func(t *testing.T) {
		t.Parallel()
		bumped := regexp.MustCompile(`actions/attest@[0-9a-f]{40} # v[0-9.]+`).ReplaceAllString(text, "actions/attest@"+strings.Repeat("a", 40)+" # v9.0.0")
		if bumped == text {
			t.Fatal("the change changed nothing")
		}
		if got := usesProblems(strings.Split(bumped, "\n")); len(got) != 0 {
			t.Errorf("refuses the publish job with actions/attest moved: %q", got)
		}
	})
}

// swapLines returns text with the first line that starts with a swapped
// with the first line that starts with b.
func swapLines(text, a, b string) string {
	lines := strings.Split(text, "\n")
	i := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, a) })
	j := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, b) })
	if i < 0 || j < 0 {
		return text
	}
	lines[i], lines[j] = lines[j], lines[i]
	return strings.Join(lines, "\n")
}

// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

// The staged index's bytes as the publish job fetches them, and the
// verification it runs on them before it moves :<app>: the one every
// app's CI runs, divbast/ci's frankenphpref.Verify.
const stagedIndex = "/tmp/staged-index.json"

var verifyFlags = []string{
	"--repo divbast/frankenphp",
	"--signer-workflow divbast/frankenphp/.github/workflows/build.yml@refs/heads/main",
	"--source-ref refs/heads/main",
}

// createdFromRun is how the staged index's created annotation is dated:
// by when the run, or this attempt at it, started. The commit's time would
// date a daily republish of an unchanged commit by that commit.
const createdFromRun = `created=$(gh api "repos/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}" --jq .run_started_at)`

// tagFlag matches an imagetools create -t or --tag, and the tag it names.
var tagFlag = regexp.MustCompile(`(?:^|\s)(?:-t|--tag)(?:\s+|=)("[^"]*"|\S+)`)

// TestPublishAttestsBeforePromote checks that the publish job moves
// :<app> only once the index it moves it to is attested and that
// attestation verifies, so a failure on the way leaves the tag where it
// was and a re-run of the job publishes again:
//
//   - the job pushes each architecture, stages the index under
//     :<app>-staging alone, dated by createdFromRun, attests the staged
//     digest, verifies it as a file fetched by that digest and hashed
//     back to it, moves :<app> to the digest from one source with no
//     annotations, so the bytes and the digest stay the attested ones,
//     and then checks the tag anonymously, in that order;
//   - no other step names :<app> as a tag or as a destination;
//   - a last step, run on a failure that came before the move, says no
//     public tag moved and that re-running the failed job publishes.
func TestPublishAttestsBeforePromote(t *testing.T) {
	t.Parallel()
	job := jobLines(readLines(t, ".github", "workflows", "build.yml"), "publish")
	if job == nil {
		t.Fatal("build.yml has no publish job")
	}
	steps := stepLines(job)
	const fromStage = "DIGEST: ${{ steps.stage.outputs.digest }}"

	order := []struct {
		what string
		find func([]string) bool
		want []string
	}{
		{
			"pushes each architecture",
			func(s []string) bool {
				return mentions(s, "skopeo copy") && mentions(s, `"docker://${IMAGE}:${APP}-${arch}"`)
			},
			nil,
		},
		{
			"stages the index",
			hasID("stage"),
			[]string{
				`-t "${IMAGE}:${APP}-staging"`,
				`echo "digest=${digest}" >> "${GITHUB_OUTPUT}"`,
				createdFromRun,
			},
		},
		{
			"attests the staged index",
			func(s []string) bool { return mentions(s, "uses: actions/attest@") },
			[]string{"subject-name: ghcr.io/divbast/frankenphp", "subject-digest: ${{ steps.stage.outputs.digest }}", "push-to-registry: true"},
		},
		{
			"verifies the attestation",
			hasID("verify"),
			append([]string{
				fromStage,
				`skopeo inspect --raw --no-creds "docker://${IMAGE}@${DIGEST}" >` + stagedIndex,
				`if [ "sha256:${fetched%% *}" != "${DIGEST}" ]; then`,
				`DOCKER_CONFIG="${empty}" gh attestation verify ` + stagedIndex,
			}, verifyFlags...),
		},
		{
			"moves the tag",
			hasID("promote"),
			[]string{fromStage, `run: docker buildx imagetools create -t "${IMAGE}:${APP}" "${IMAGE}@${DIGEST}"`},
		},
		{
			"checks the tag anonymously",
			func(s []string) bool {
				return mentions(s, `skopeo inspect --raw --no-creds "docker://${IMAGE}:${APP}"`)
			},
			[]string{fromStage},
		},
		{
			"says what a failure before the move left",
			func(s []string) bool { return mentions(s, "steps.promote.outcome") },
			[]string{"if: failure() && steps.decide.outputs.publish == 'true' && steps.promote.outcome == 'skipped'"},
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
	if at[len(at)-1] != len(steps)-1 {
		t.Error("the step that says what a failure left is not the publish job's last")
	}

	promote, last := steps[at[4]], steps[at[6]]
	if mentions(promote, "--annotation") {
		t.Error("the step that moves the tag passes --annotation, so buildx writes a new index with a digest nothing attested")
	}
	message := strings.Join(trimmed(last), "\n")
	for _, want := range []string{"No public tag moved", "Re-running the failed job publishes"} {
		if !strings.Contains(message, want) {
			t.Errorf("the step that says what a failure left does not say %q", want)
		}
	}

	// Which steps tag what, and which name :<app> as a destination.
	for i, step := range steps {
		var tags []string
		for _, line := range step {
			for _, m := range tagFlag.FindAllStringSubmatch(line, -1) {
				tags = append(tags, m[1])
			}
		}
		var want []string
		switch i {
		case at[1]:
			want = []string{`"${IMAGE}:${APP}-staging"`}
		case at[4]:
			want = []string{`"${IMAGE}:${APP}"`}
		}
		if !slices.Equal(tags, want) {
			t.Errorf("publish step %d (%s) tags %q, not %q", i+1, strings.TrimSpace(step[0]), tags, want)
		}
		for _, line := range step {
			if strings.Contains(line, `docker://${IMAGE}:${APP}"`) && !strings.Contains(line, "no-creds") {
				t.Errorf("publish step %d (%s) writes to %s: %q", i+1, strings.TrimSpace(step[0]), "${IMAGE}:${APP}", strings.TrimSpace(line))
			}
		}
	}
}

// The shared Renovate managers' patterns for the two build tools the
// workflow pins (divbast/renovate-config's default.json, customManagers),
// as Renovate reads them: a pin in any other shape is one Renovate never
// moves.
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

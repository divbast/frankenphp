// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestBuildAppTakesOnlyAFineGrainedOrCIToken runs the justfile's build-app
// recipe, which builds through this command, with testdata/engine.sh as
// its engine, for each kind of token GITHUB_TOKEN can hold. Every build
// script spc runs can read the token, so the recipe takes only a
// fine-grained personal access token (github_pat_) or the job token
// Actions issues (ghs_). divbast/ci's shared _github-token recipe, which
// build-app runs first, refuses anything else before the engine runs, in
// one line that names the kind it saw and never the token.
func TestBuildAppTakesOnlyAFineGrainedOrCIToken(t *testing.T) {
	t.Parallel()

	just, err := exec.LookPath("just")
	if err != nil {
		t.Fatalf("just runs the recipe, and is not on PATH; run the tests with `just test`: %v", err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// _github-token is divbast/ci's, imported from its checkout
	if _, statErr := os.Stat(filepath.Join(root, ".ci", "just", "common.just")); statErr != nil {
		t.Fatalf("build-app's token check is divbast/ci's, and .ci has none; run `just ci-checkout`: %v", statErr)
	}
	engine, err := filepath.Abs(filepath.Join("testdata", "engine.sh"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		// prefix starts the made-up token GITHUB_TOKEN holds, which is
		// empty when prefix is.
		prefix string
		unset  bool
		// refused is the kind the refusal must name; empty means the
		// recipe goes on to build.
		refused string
	}{
		{name: "fine-grained", prefix: "github_pat_"},
		{name: "actions", prefix: "ghs_"},
		{name: "gh", prefix: "gho_", refused: "a gh/OAuth token (gho_)"},
		{name: "classic", prefix: "ghp_", refused: "a classic personal access token (ghp_)"},
		{name: "app-user", prefix: "ghu_", refused: "a GitHub App user token (ghu_)"},
		// A token from before GitHub's tokens carried a prefix
		{name: "app-refresh", prefix: "ghr_", refused: "a GitHub App refresh token (ghr_)"},
		{name: "unknown", prefix: "0123456789abcdef", refused: "not a GitHub token of any known kind"},
		{name: "empty", refused: "unset or empty"},
		{name: "unset", unset: true, refused: "unset or empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			token := ""
			if tt.prefix != "" {
				token = tt.prefix + "0made0up0for0the0test0" + tt.name
			}
			log := filepath.Join(t.TempDir(), "engine.log")
			cmd := exec.CommandContext(t.Context(), just, //nolint:gosec // G204: just, running this repository's own justfile
				"--justfile", filepath.Join(root, "justfile"), "--working-directory", root,
				"build-app", "librespeed")
			cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
				name, _, _ := strings.Cut(kv, "=")
				return name == "GITHUB_TOKEN" || name == "ENGINE" || name == "STUB_ENGINE_LOG"
			})
			cmd.Env = append(cmd.Env, "ENGINE="+engine, "STUB_ENGINE_LOG="+log)
			if !tt.unset {
				cmd.Env = append(cmd.Env, "GITHUB_TOKEN="+token)
			}
			var stdout, stderr strings.Builder
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			runErr := cmd.Run()
			if _, ok := errors.AsType[*exec.ExitError](runErr); runErr != nil && !ok {
				t.Fatalf("just: %v", runErr)
			}
			output := stdout.String() + stderr.String()
			if token != "" && strings.Contains(output, token) {
				t.Errorf("the recipe printed the token:\n%s", output)
			}
			called, err := os.ReadFile(log) //nolint:gosec // G304: the engine's log, in the test's own TempDir
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Fatal(err)
			}

			if tt.refused != "" {
				if runErr == nil {
					t.Fatalf("build-app built with %s; want a refusal\n%s", tt.name, output)
				}
				if called != nil {
					t.Errorf("the engine ran before the refusal:\n%s", called)
				}
				var lines []string
				for line := range strings.Lines(stderr.String()) {
					if strings.HasPrefix(line, "GITHUB_TOKEN is ") {
						lines = append(lines, line)
					}
				}
				if len(lines) != 1 || !strings.Contains(lines[0], "GITHUB_TOKEN is "+tt.refused+";") ||
					!strings.Contains(lines[0], "https://github.com/divbast/ci#local-github-token") {
					t.Errorf("stderr = %q, want one line naming %q and ci's README section", stderr.String(), tt.refused)
				}
				return
			}

			if runErr != nil {
				t.Fatalf("build-app refused a %s token:\n%s", tt.name, output)
			}
			args := strings.Split(strings.TrimSuffix(string(called), "\n"), "\n")
			want := []string{"GITHUB_TOKEN is set", "build", "--target", "carrier", "--secret", "id=github_token,env=GITHUB_TOKEN"}
			if len(args) < len(want) || !slices.Equal(args[:len(want)], want) || !slices.Contains(args, "APP=librespeed") {
				t.Errorf("the engine ran with %q, want it to start %q and build APP=librespeed", args, want)
			}
			if strings.Contains(string(called), token) {
				t.Errorf("the engine was given the token as an argument:\n%s", called)
			}
			// The recipe the records stage writes down, hashed as
			// build.yml hashes it, and passed once: of two, the last
			// would win
			recipe := sha256.Sum256(slices.Concat(readFile(t, root, "Containerfile"), readFile(t, root, "apps", "librespeed.env")))
			var recipes []string
			for _, arg := range args {
				if strings.HasPrefix(arg, "RECIPE_SHA256=") {
					recipes = append(recipes, arg)
				}
			}
			if want := "RECIPE_SHA256=" + hex.EncodeToString(recipe[:]); !slices.Equal(recipes, []string{want}) {
				t.Errorf("the engine was passed %q, want only %s, the sha256 of the Containerfile and apps/librespeed.env", recipes, want)
			}
		})
	}
}

// readFile reads the repository's file at the path elems name, under root.
func readFile(t *testing.T, root string, elems ...string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{root}, elems...)...))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

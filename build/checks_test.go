// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

// Package build tests the scripts the Containerfile runs inside its build
// stages. Each runs against fixtures, a stub frankenphp binary answering
// from testdata or a static-php-cli checkout made for the test, and every
// way each script can fail is seen failing.
package build

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/divbast/frankenphp/internal/appenv"
)

// The versions the testdata binary and xcaddy report, as the Containerfile's
// ARGs pin them.
var pins = map[string]string{
	"PHP_VERSION":     "8.5.12",
	"CADDY_VERSION":   "v2.11.9",
	"CBROTLI_VERSION": "v1.0.1",
	"MERCURE_VERSION": "v1.1.6",
	"VULCAIN_VERSION": "v1.4.6",
	"XCADDY_VERSION":  "v0.4.7",
}

// The AOM release the Containerfile pins libaom to, as testdata's
// LibreSpeed download cache records it.
const libaomTag = "v3.15.3"

// testCase is one run of a script.
type testCase struct {
	name string
	// app names the testdata binary the stub answers as, LibreSpeed's when
	// empty, and edits rewrite its canned answers.
	app   string
	edits []edit
	// set adds or replaces inputs, and unset removes them.
	set   map[string]string
	unset []string
	// mutate changes the fixture on disk, given the inputs naming it.
	mutate func(t *testing.T, env map[string]string)
	// want holds one string per ERROR line the script must print, each
	// naming the ARG, extension or file at fault. Empty means it passes.
	want []string
	// say, when set, is what a passing script's stdout must hold.
	say string
	// hints names the advice a failing pinned-versions.sh must follow its
	// ERROR lines with, as keys of pinnedHints; it must give no other.
	hints []string
}

// pinnedHints maps each kind of pinned-versions.sh failure to a phrase only
// its advice holds. Each kind needs different advice: a lifted module's pin
// is raised, a replaced module's never is, and neither is xcaddy's when spc
// built its own.
var pinnedHints = map[string]string{
	"php":      "craft.yml's php-version",
	"lifted":   "minimum version selection",
	"missing":  "checks nothing",
	"replaced": "remove the replace",
	"xcaddy":   "don't raise XCADDY_VERSION",
}

// precraftCache is the download cache the go-xcaddy step leaves before the
// craft runs, captured from that step: install-pkg writes only go-xcaddy.
const precraftCache = "testdata/precraft-spc-cache.json"

// edit rewrites one canned answer. from must match exactly once, so that a
// fixture which drifts fails the test rather than passing it for the wrong
// reason.
type edit struct {
	file, from, to string
}

// result is how a script ended.
type result struct {
	code           int
	stdout, stderr string
}

func TestPinnedVersions(t *testing.T) {
	t.Parallel()

	tests := []testCase{
		{name: "ok"},
		{
			name:  "php-mismatch",
			edits: []edit{{"version", `PHP 8\.5\.12`, "PHP 8.5.11"}},
			want:  []string{"PHP_VERSION"},
			hints: []string{"php"},
		},
		{
			name:  "php-unreported",
			edits: []edit{{"version", ` PHP 8\.5\.12`, ""}},
			want:  []string{"PHP_VERSION"},
			hints: []string{"php"},
		},
		{
			name:  "caddy-lifted",
			edits: []edit{{"build-info", `(?m)^(dep\tgithub\.com/caddyserver/caddy/v2\t)v2\.11\.9\t`, "${1}v2.11.10\t"}},
			want:  []string{"CADDY_VERSION"},
			hints: []string{"lifted"},
		},
		{
			name:  "cbrotli-lifted",
			edits: []edit{{"build-info", `(?m)^(dep\tgithub\.com/dunglas/caddy-cbrotli\t)v1\.0\.1\t`, "${1}v1.0.2\t"}},
			want:  []string{"CBROTLI_VERSION"},
			hints: []string{"lifted"},
		},
		{
			name:  "mercure-lifted",
			edits: []edit{{"build-info", `(?m)^(dep\tgithub\.com/dunglas/mercure/caddy\t)v1\.1\.6\t`, "${1}v1.1.7\t"}},
			want:  []string{"MERCURE_VERSION"},
			hints: []string{"lifted"},
		},
		{
			name:  "plugin-missing",
			edits: []edit{{"build-info", `(?m)^dep\tgithub\.com/dunglas/vulcain/caddy\t.*\n`, ""}},
			want:  []string{"VULCAIN_VERSION"},
			hints: []string{"missing"},
		},
		{
			// A replace line follows the module it replaces, and the
			// replacement is what the binary holds.
			name:  "caddy-replaced",
			edits: []edit{{"build-info", `(?m)^(dep\tgithub\.com/caddyserver/caddy/v2\t.*\n)`, "${1}=>\tgithub.com/example/caddy/v2\tv2.11.8\th1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=\n"}},
			want:  []string{"CADDY_VERSION"},
			hints: []string{"replaced"},
		},
		{
			name:  "plugin-replaced-locally",
			edits: []edit{{"build-info", `(?m)^(dep\tgithub\.com/dunglas/mercure/caddy\t.*\n)`, "${1}=>\t/src/mercure/caddy\t(devel)\t\n"}},
			want:  []string{"MERCURE_VERSION"},
			hints: []string{"replaced"},
		},
		{
			// A fork that calls itself the pinned version is still not the
			// module the pin names.
			name:  "replaced-at-the-pin",
			edits: []edit{{"build-info", `(?m)^(dep\tgithub\.com/dunglas/vulcain/caddy\t.*\n)`, "${1}=>\tgithub.com/example/vulcain/caddy\tv1.4.6\th1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=\n"}},
			want:  []string{"VULCAIN_VERSION"},
			hints: []string{"replaced"},
		},
		{
			name:  "xcaddy-moved",
			edits: []edit{{"xcaddy", `(?m)^(\tmod\tgithub\.com/caddyserver/xcaddy\t)v0\.4\.7\t`, "${1}v0.4.8-0.20261101000000-0123456789ab\t"}},
			want:  []string{"XCADDY_VERSION"},
			hints: []string{"xcaddy"},
		},
		{
			// Each failure keeps its own advice when several come at once.
			name: "lifted-and-xcaddy-moved",
			edits: []edit{
				{"build-info", `(?m)^(dep\tgithub\.com/caddyserver/caddy/v2\t)v2\.11\.9\t`, "${1}v2.11.10\t"},
				{"xcaddy", `(?m)^(\tmod\tgithub\.com/caddyserver/xcaddy\t)v0\.4\.7\t`, "${1}v0.4.8-0.20261101000000-0123456789ab\t"},
			},
			want:  []string{"CADDY_VERSION", "XCADDY_VERSION"},
			hints: []string{"lifted", "xcaddy"},
		},
		{
			name:  "xcaddy-devel",
			edits: []edit{{"xcaddy", `(?m)^(\tmod\tgithub\.com/caddyserver/xcaddy\t)v0\.4\.7\t.*$`, "${1}(devel)\t"}},
			want:  []string{"XCADDY_VERSION"},
			hints: []string{"xcaddy"},
		},
		{
			name:  "xcaddy-unreported",
			edits: []edit{{"xcaddy", `(?m)^\tmod\tgithub\.com/caddyserver/xcaddy\t.*\n`, ""}},
			want:  []string{"XCADDY_VERSION"},
			hints: []string{"xcaddy"},
		},
		{
			name: "go-fails",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				remove(t, filepath.Join(env["STUB_ANSWERS"], "xcaddy"))
			},
			want: []string{"XCADDY_BIN"},
		},
		{
			name:  "pin-unset",
			unset: []string{"CADDY_VERSION"},
			want:  []string{"CADDY_VERSION is empty"},
		},
		{
			name:  "xcaddy-pin-unset",
			unset: []string{"XCADDY_VERSION"},
			want:  []string{"XCADDY_VERSION is empty"},
		},
		{
			name: "no-binary",
			set:  map[string]string{"FRANKENPHP_BIN": "/nonexistent/frankenphp"},
			want: []string{"FRANKENPHP_BIN"},
		},
		{
			name: "no-go",
			set:  map[string]string{"GO_BIN": "/nonexistent/go"},
			want: []string{"GO_BIN"},
		},
		{
			name: "no-xcaddy",
			set:  map[string]string{"XCADDY_BIN": "/nonexistent/xcaddy"},
			want: []string{"XCADDY_BIN"},
		},
	}
	goStub, err := filepath.Abs(filepath.Join("testdata", "go.sh"))
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := stub(t, tt.app, tt.edits)
			maps.Copy(env, pins)
			// spc's go-xcaddy package: the go that reads xcaddy's build
			// info, and xcaddy, which only that go looks at
			env["GO_BIN"] = goStub
			env["XCADDY_BIN"] = filepath.Join(t.TempDir(), "xcaddy")
			write(t, env["XCADDY_BIN"], "\x7fELF not really")
			r := run(t, "pinned-versions", tt.inputs(t, env))
			check(t, r, tt.want)
			for kind, phrase := range pinnedHints {
				if want, got := slices.Contains(tt.hints, kind), strings.Contains(r.stderr, phrase); want != got {
					t.Errorf("advice for a %s pin (%q): given %t, want %t; stderr:\n%s", kind, phrase, got, want, r.stderr)
				}
			}
		})
	}
}

func TestPinnedLibaom(t *testing.T) {
	t.Parallel()

	tests := []testCase{
		{name: "ok", say: "dev-" + libaomTag + "+"},
		{
			// Baikal builds no gd, so nothing brings libaom.
			name: "absent",
			app:  "baikal",
			say:  "no libaom",
		},
		{
			// What spc fetches when it ignored the custom-git line: the
			// branch libaom's own config names.
			name:  "branch-head",
			edits: []edit{{"spc-cache.json", `"version": "dev-v3\.15\.3\+b141d9c"`, `"version": "dev-main+8a10385"`}},
			want:  []string{"dev-main+8a10385"},
		},
		{
			name:  "other-tag",
			edits: []edit{{"spc-cache.json", `"version": "dev-v3\.15\.3\+b141d9c"`, `"version": "dev-v3.15.2+af3dc9a"`}},
			want:  []string{"dev-v3.15.2+af3dc9a"},
		},
		{
			// The tag ends at the +: v3.15.30 is not v3.15.3.
			name:  "longer-tag",
			edits: []edit{{"spc-cache.json", `"version": "dev-v3\.15\.3\+b141d9c"`, `"version": "dev-v3.15.30+0123456"`}},
			want:  []string{"dev-v3.15.30+0123456"},
		},
		{
			name:  "unversioned",
			edits: []edit{{"spc-cache.json", `"version": "dev-v3\.15\.3\+b141d9c"`, `"version": null`}},
			want:  []string{"no version"},
		},
		{
			// What the go-xcaddy step leaves when the craft never ran, or
			// never wrote its cache: no app built, so no libaom to pass on.
			name: "not-the-crafts-cache",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				write(t, env["SPC_CACHE"], read(t, precraftCache))
			},
			want: []string{"php-src"},
		},
		{
			name: "empty-object",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				write(t, env["SPC_CACHE"], "{}\n")
			},
			want: []string{"php-src"},
		},
		{
			name: "libaom-not-an-object",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				write(t, env["SPC_CACHE"], `{"php-src": {"source": {"version": "8.5.12"}}, "libaom": "dev-v3.15.3+b141d9c"}`+"\n")
			},
			want: []string{"entry for libaom"},
		},
		{
			name: "not-json",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				write(t, env["SPC_CACHE"], "libaom: dev-v3.15.3+b141d9c\n")
			},
			want: []string{"not spc's download cache"},
		},
		{
			name: "not-an-object",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				write(t, env["SPC_CACHE"], `["libaom"]`+"\n")
			},
			want: []string{"not spc's download cache"},
		},
		{
			name: "no-cache",
			set:  map[string]string{"SPC_CACHE": "/nonexistent/.cache.json"},
			want: []string{"SPC_CACHE"},
		},
		{
			name:  "tag-unset",
			unset: []string{"LIBAOM_TAG"},
			want:  []string{"LIBAOM_TAG is empty"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// testdata/<app>/spc-cache.json, captured from that app's
			// build, as spc leaves it in dist/downloads
			env := stub(t, tt.app, tt.edits)
			env["SPC_CACHE"] = filepath.Join(env["STUB_ANSWERS"], "spc-cache.json")
			env["LIBAOM_TAG"] = libaomTag
			r := run(t, "pinned-libaom", tt.inputs(t, env))
			check(t, r, tt.want)
			// A wrong libaom is fixed in the Containerfile's pin or its
			// custom-git line, so each such complaint names LIBAOM_TAG.
			for _, line := range errorLines(r.stderr) {
				if strings.Contains(line, "dev-") && !strings.Contains(line, "LIBAOM_TAG") {
					t.Errorf("ERROR line does not name LIBAOM_TAG: %q", line)
				}
			}
			if tt.say != "" && !strings.Contains(r.stdout, tt.say) {
				t.Errorf("stdout does not say %q:\n%s", tt.say, r.stdout)
			}
		})
	}
}

func TestPinnedSPC(t *testing.T) {
	t.Parallel()

	tests := []testCase{
		{name: "ok"},
		{
			// What build-static.sh leaves when dist/spc was not executable:
			// its own download there, which records nothing.
			name: "nightly-download",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				write(t, env["SPC_WRAPPER"], "\x7fELF\x02\x01\x01")
				remove(t, env["SPC_CALLS"])
			},
			want: []string{"not the wrapper", "never ran spc craft"},
		},
		{
			name: "never-crafted",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				write(t, env["SPC_CALLS"], "doctor\n")
			},
			want: []string{"never ran spc craft"},
		},
		{
			name: "v3-clone",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				write(t, filepath.Join(env["SPC_CLONE"], "bin", "spc"), "<?php\n")
			},
			want: []string{"cloned static-php-cli"},
		},
		{
			name: "commit-moved",
			set:  map[string]string{"SPC_COMMIT": "ff04d4af9ba44f6c194239d733e0529cc6ddecea"},
			want: []string{"pins ff04d4af9ba44f6c194239d733e0529cc6ddecea"},
		},
		{
			name: "checkout-changed",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				write(t, filepath.Join(env["SPC_SRC"], "bin", "spc"), "<?php exit(0);\n")
			},
			want: []string{"changes to tracked files"},
		},
		{
			name: "no-checkout",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				remove(t, filepath.Join(env["SPC_SRC"], ".git"))
			},
			want: []string{"no checkout"},
		},
		{
			name:  "commit-unset",
			unset: []string{"SPC_COMMIT"},
			want:  []string{"SPC_COMMIT is empty"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := run(t, "pinned-spc", tt.inputs(t, spc(t)))
			check(t, r, tt.want)
			// Whatever went wrong, the fix is to the spc that SPC_COMMIT
			// pins, so every complaint names it.
			for _, line := range errorLines(r.stderr) {
				if !strings.Contains(line, "SPC_COMMIT") {
					t.Errorf("ERROR line does not name SPC_COMMIT: %q", line)
				}
			}
		})
	}
}

func TestExtensions(t *testing.T) {
	t.Parallel()

	tests := []testCase{
		{name: "ok"},
		{name: "baikal", app: "baikal"},
		{
			name:  "extra",
			edits: []edit{{"extensions", `(?m)^Phar$`, "Phar\nintl"}},
			want:  []string{"intl"},
		},
		{
			name:  "missing",
			edits: []edit{{"extensions", `(?m)^Phar\n`, ""}},
			want:  []string{"phar"},
		},
		{
			name:  "gd-feature-missing",
			edits: []edit{{"gd", `(?m)^PNG Support\n`, ""}},
			want:  []string{"PNG Support"},
		},
		{
			name: "gd-absent",
			edits: []edit{
				{"extensions", `(?m)^gd\n`, ""},
				{"gd", `(?s).+`, ""},
			},
			want: []string{"names gd,", "FreeType Support", "PNG Support"},
		},
		{
			name:  "expected-unset",
			unset: []string{"EXPECTED_EXTENSIONS"},
			want:  []string{"EXPECTED_EXTENSIONS is empty"},
		},
		{
			name: "no-binary",
			set:  map[string]string{"FRANKENPHP_BIN": "/nonexistent/frankenphp"},
			want: []string{"FRANKENPHP_BIN"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// What the build passes: the app's own apps/<app>.env,
			// checked against what its published binary loads.
			app := appInputs(t, tt.app)
			env := stub(t, tt.app, tt.edits)
			env["EXPECTED_EXTENSIONS"] = app.ExpectedExtensions
			env["REQUIRED_GD_FEATURES"] = app.RequiredGDFeatures
			check(t, run(t, "extensions", tt.inputs(t, env)), tt.want)
		})
	}
}

func TestRecords(t *testing.T) {
	t.Parallel()

	const (
		cache    = `{"php-src":{"source":{"version":"8.5.12"}}}` + "\n"
		manifest = `{"schema-version":1}` + "\n"
		// Inputs of the shape the build passes, and none of them the
		// Containerfile's own values, so that a records.sh writing those
		// whatever it is given fails. The recipe is sha256sum's hash
		// alone, without its file name, as build.yml passes it.
		spcCommit = "0123456789abcdef0123456789abcdef01234567"
		builder   = "registry.example.invalid/builder:test@sha256:00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
		recipe    = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"
		craft     = "php-version: \"8.5.12\"\nextensions: \"ctype,gd\"\n"
	)
	tests := []testCase{
		{name: "ok"},
		{name: "spc-unset", unset: []string{"SPC_COMMIT"}, want: []string{"SPC_COMMIT"}},
		// A branch, which names no one commit
		{name: "spc-not-a-commit", set: map[string]string{"SPC_COMMIT": "v3"}, want: []string{"SPC_COMMIT"}},
		{name: "builder-unset", unset: []string{"BUILDER_IMAGE"}, want: []string{"BUILDER_IMAGE"}},
		// A tag alone, which moves
		{name: "builder-without-digest", set: map[string]string{"BUILDER_IMAGE": "dunglas/frankenphp:static-builder-gnu"}, want: []string{"BUILDER_IMAGE"}},
		{name: "recipe-unset", unset: []string{"RECIPE_SHA256"}, want: []string{"RECIPE_SHA256"}},
		// sha256sum's whole line, its file name included
		{name: "recipe-with-file-name", set: map[string]string{"RECIPE_SHA256": recipe + "  -"}, want: []string{"RECIPE_SHA256"}},
		{
			name: "no-craft",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				remove(t, env["CRAFT"])
			},
			want: []string{"rendered craft.yml"},
		},
		{
			name: "craft-empty",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				write(t, env["CRAFT"], "")
			},
			want: []string{"rendered craft.yml"},
		},
		{
			name: "no-download-cache",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				remove(t, env["SPC_CACHE"])
			},
			want: []string{"download cache"},
		},
		{
			name: "download-cache-not-the-crafts",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				write(t, env["SPC_CACHE"], read(t, precraftCache))
			},
			want: []string{"php-src"},
		},
		{
			name: "download-cache-empty-object",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				write(t, env["SPC_CACHE"], "{}\n")
			},
			want: []string{"php-src"},
		},
		{
			name: "download-cache-not-json",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				write(t, env["SPC_CACHE"], "php-src\n")
			},
			want: []string{"php-src"},
		},
		{
			name: "no-build-manifest",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				remove(t, env["BUILD_MANIFEST"])
			},
			want: []string{"build manifest"},
		},
		{
			name: "records-exist",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				write(t, filepath.Join(env["RECORDS"], "app.txt"), "baikal\n")
			},
			want: []string{"already exists"},
		},
		{
			// The binary failing part way through, once writing has begun
			name: "binary-fails",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				remove(t, filepath.Join(env["STUB_ANSWERS"], "build-info"))
			},
			want: []string{"build-info.txt"},
		},
		{
			// go failing to read xcaddy's build info, once writing has begun
			name: "go-fails",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				remove(t, filepath.Join(env["STUB_ANSWERS"], "xcaddy"))
			},
			want: []string{"xcaddy.txt"},
		},
		{
			name: "no-xcaddy",
			mutate: func(t *testing.T, env map[string]string) {
				t.Helper()
				remove(t, env["XCADDY_BIN"])
			},
			want: []string{"XCADDY_BIN"},
		},
		{
			name: "no-go",
			set:  map[string]string{"GO_BIN": "/nonexistent/go"},
			want: []string{"GO_BIN"},
		},
		{
			name:  "app-unset",
			unset: []string{"APP"},
			want:  []string{"APP is empty"},
		},
		{
			name: "no-binary",
			set:  map[string]string{"FRANKENPHP_BIN": "/nonexistent/frankenphp"},
			want: []string{"FRANKENPHP_BIN"},
		},
	}
	goStub, err := filepath.Abs(filepath.Join("testdata", "go.sh"))
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := stub(t, tt.app, tt.edits)
			dist := t.TempDir()
			env["APP"] = "librespeed"
			env["SPC_CACHE"] = filepath.Join(dist, "downloads", ".cache.json")
			env["BUILD_MANIFEST"] = filepath.Join(dist, "buildroot", "build-manifest.json")
			// spc's go-xcaddy package: the go that reads xcaddy's build
			// info, and xcaddy, which only that go looks at
			env["GO_BIN"] = goStub
			env["XCADDY_BIN"] = filepath.Join(dist, "pkgroot", "go-xcaddy", "bin", "xcaddy")
			env["RECORDS"] = filepath.Join(dist, "records")
			// The build's own inputs, which the records stage declares as
			// ARGs, and the craft.yml build-static.sh rendered
			env["SPC_COMMIT"] = spcCommit
			env["BUILDER_IMAGE"] = builder
			env["RECIPE_SHA256"] = recipe
			env["CRAFT"] = filepath.Join(dist, "craft.yml")
			write(t, env["SPC_CACHE"], cache)
			write(t, env["BUILD_MANIFEST"], manifest)
			write(t, env["XCADDY_BIN"], "\x7fELF not really")
			write(t, env["CRAFT"], craft)

			env = tt.inputs(t, env)
			before := tree(t, dist)
			check(t, run(t, "records", env), tt.want)
			if len(tt.want) > 0 {
				// A record set is whole or absent: a failure leaves
				// nothing behind for the carrier to ship part of.
				if after := tree(t, dist); !slices.Equal(after, before) {
					t.Errorf("after a failure the build holds %q, want %q as before", after, before)
				}
				return
			}

			extensions := strings.Split(strings.TrimSuffix(read(t, "testdata/librespeed/extensions"), "\n"), "\n")
			slices.Sort(extensions)
			want := map[string]string{
				"app.txt":             "librespeed\n",
				"version.txt":         read(t, "testdata/librespeed/version"),
				"build-info.txt":      read(t, "testdata/librespeed/build-info"),
				"xcaddy.txt":          read(t, "testdata/librespeed/xcaddy"),
				"extensions.txt":      strings.Join(extensions, "\n") + "\n",
				"spc-cache.json":      cache,
				"build-manifest.json": manifest,
				"spc.txt":             spcCommit + "\n",
				"builder.txt":         builder + "\n",
				"craft.yml":           craft,
				"recipe.txt":          recipe + "\n",
			}
			entries, err := os.ReadDir(env["RECORDS"])
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, e := range entries {
				got = append(got, e.Name())
			}
			if names := slices.Sorted(maps.Keys(want)); !slices.Equal(got, names) {
				t.Fatalf("records = %q, want %q", got, names)
			}
			for name, content := range want {
				if got := read(t, filepath.Join(env["RECORDS"], name)); got != content {
					t.Errorf("%s = %q, want %q", name, got, content)
				}
			}
		})
	}
}

// appInputs reads apps/<app>.env, LibreSpeed's when app is empty, as every
// build does.
func appInputs(t *testing.T, app string) appenv.Inputs {
	t.Helper()

	if app == "" {
		app = "librespeed"
	}
	root, err := os.OpenRoot(filepath.Join("..", "apps"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	in, err := appenv.Load(root, app)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// stub copies testdata/<app>'s canned answers, LibreSpeed's when app is
// empty, where the stub binary reads them, applies edits to them, and
// returns the inputs that point a script at the stub.
func stub(t *testing.T, app string, edits []edit) map[string]string {
	t.Helper()

	if app == "" {
		app = "librespeed"
	}
	answers := t.TempDir()
	if err := os.CopyFS(answers, os.DirFS(filepath.Join("testdata", app))); err != nil {
		t.Fatal(err)
	}
	for _, e := range edits {
		path := filepath.Join(answers, e.file)
		data := read(t, path)
		re := regexp.MustCompile(e.from)
		if n := len(re.FindAllStringIndex(data, -1)); n != 1 {
			t.Fatalf("edit %q matches testdata/%s/%s %d times, want once", e.from, app, e.file, n)
		}
		write(t, path, re.ReplaceAllString(data, e.to))
	}
	bin, err := filepath.Abs(filepath.Join("testdata", "frankenphp.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{"FRANKENPHP_BIN": bin, "STUB_ANSWERS": answers}
}

// spc makes what the Containerfile leaves for pinned-spc.sh: a
// static-php-cli checkout at one commit, the wrapper around it at dist/spc
// and the wrapper's record of one craft run. It returns the inputs naming
// them.
func spc(t *testing.T) map[string]string {
	t.Helper()

	root := t.TempDir()
	src := filepath.Join(root, "static-php-cli")
	dist := filepath.Join(root, "dist")
	write(t, filepath.Join(src, "bin", "spc"), "<?php\n")
	git(t, "init", "-q", src)
	git(t, "-C", src, "add", ".")
	git(t, "-C", src, "commit", "-q", "-m", "spc")
	// The wrapper, line for line as the Containerfile writes it.
	write(t, filepath.Join(dist, "spc"), "#!/bin/sh\n"+
		`echo "$1" >>`+filepath.Join(dist, "spc.calls")+"\n"+
		"exec php "+src+`/bin/spc "$@"`+"\n")
	write(t, filepath.Join(dist, "spc.calls"), "craft\n")
	return map[string]string{
		"SPC_COMMIT":  strings.TrimSpace(git(t, "-C", src, "rev-parse", "HEAD")),
		"SPC_SRC":     src,
		"SPC_WRAPPER": filepath.Join(dist, "spc"),
		"SPC_CALLS":   filepath.Join(dist, "spc.calls"),
		"SPC_CLONE":   filepath.Join(dist, "static-php-cli"),
	}
}

// tree lists every path under root, sorted.
func tree(t *testing.T, root string) []string {
	t.Helper()

	var paths []string
	err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		paths = append(paths, path)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// inputs applies tt's input changes and fixture mutation to env.
func (tt *testCase) inputs(t *testing.T, env map[string]string) map[string]string {
	t.Helper()

	maps.Copy(env, tt.set)
	for _, name := range tt.unset {
		delete(env, name)
	}
	if tt.mutate != nil {
		tt.mutate(t, env)
	}
	return env
}

// run runs ./<script>.sh with env as its environment, beside a PATH and a
// git that reads no configuration but its own.
func run(t *testing.T, script string, env map[string]string) result {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "./"+script+".sh") //nolint:gosec // G204: the script is one of this package's own
	cmd.Env = isolated(t)
	for name, value := range env {
		cmd.Env = append(cmd.Env, name+"="+value)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); !ok {
			t.Fatalf("%s.sh: %v", script, err)
		}
	}
	return result{cmd.ProcessState.ExitCode(), stdout.String(), stderr.String()}
}

// check fails t unless r passed silently, when want is empty, or else
// failed with exactly one ERROR line for each string in want, naming it.
func check(t *testing.T, r result, want []string) {
	t.Helper()

	if len(want) == 0 {
		if r.code != 0 || r.stderr != "" {
			t.Fatalf("exit %d, want 0 with nothing on stderr; stderr:\n%s", r.code, r.stderr)
		}
		return
	}
	if r.code == 0 {
		t.Fatalf("exit 0, want a failure naming %q; stdout:\n%s", want, r.stdout)
	}
	lines := errorLines(r.stderr)
	if len(lines) != len(want) {
		t.Errorf("%d ERROR lines, want %d naming %q; stderr:\n%s", len(lines), len(want), want, r.stderr)
	}
	for _, w := range want {
		if !slices.ContainsFunc(lines, func(line string) bool { return strings.Contains(line, w) }) {
			t.Errorf("no ERROR line names %q; stderr:\n%s", w, r.stderr)
		}
	}
}

// errorLines returns the lines of stderr that report a failure.
func errorLines(stderr string) []string {
	var lines []string
	for line := range strings.Lines(stderr) {
		if strings.HasPrefix(line, "ERROR: ") {
			lines = append(lines, line)
		}
	}
	return lines
}

// isolated is the environment every command here starts from, so nothing
// of the host's reaches a script but its PATH.
func isolated(t *testing.T) []string {
	t.Helper()

	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
	}
}

// git runs git with args, and returns its output.
func git(t *testing.T, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...) //nolint:gosec // G204: git, with this file's own arguments
	cmd.Env = append(isolated(t),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %q: %v", args, err)
	}
	return string(out)
}

func read(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // G304: testdata, or a file the test made in its own TempDir
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func write(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func remove(t *testing.T, path string) {
	t.Helper()

	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
}

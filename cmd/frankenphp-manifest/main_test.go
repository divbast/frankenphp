// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/divbast/frankenphp/internal/manifest"
	"github.com/divbast/frankenphp/internal/manifest/manifesttest"
)

// records reads one app's records from the manifest package's testdata, as
// an arch build would have written them.
func records(t *testing.T, app, arch string) map[string][]byte {
	t.Helper()
	dir := filepath.Join("..", "..", "internal", "manifest", "testdata", app)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".license") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // G304: a file of the test's own testdata
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = data
	}
	files["build-info.txt"] = bytes.Replace(files["build-info.txt"], []byte("GOARCH=arm64"), []byte("GOARCH="+arch), 1)
	return files
}

// carrier writes one app's carrier for arch, as the build exports it.
func carrier(t *testing.T, app, arch string, edit func(*manifesttest.Image)) string {
	t.Helper()
	img := manifesttest.Image{Arch: arch, Records: records(t, app, arch)}
	if edit != nil {
		edit(&img)
	}
	return manifesttest.Write(t, manifesttest.Archive{Images: []manifesttest.Image{img}})
}

// published writes one app's published index, with an image for each of
// arches, each changed by edit if it is not nil.
func published(t *testing.T, app string, edit func(*manifesttest.Image), arches ...string) string {
	t.Helper()
	var a manifesttest.Archive
	for _, arch := range arches {
		img := manifesttest.Image{Arch: arch, Records: records(t, app, arch)}
		if edit != nil {
			edit(&img)
		}
		a.Images = append(a.Images, img)
	}
	return manifesttest.Write(t, a)
}

// fixture builds a test's arguments.
type fixture struct{ t *testing.T }

func (f fixture) carrier(app, arch string, edit func(*manifesttest.Image)) string {
	f.t.Helper()
	return carrier(f.t, app, arch, edit)
}

func (f fixture) published(app string, edit func(*manifesttest.Image), arches ...string) string {
	f.t.Helper()
	return published(f.t, app, edit, arches...)
}

func (f fixture) json(m *manifest.Manifest) string {
	f.t.Helper()
	return writeJSON(f.t, m)
}

func (f fixture) file(data string) string {
	f.t.Helper()
	return writeFile(f.t, data)
}

// moved is a carrier whose libaom moved to another commit.
func moved(img *manifesttest.Image) {
	img.Records["spc-cache.json"] = bytes.Replace(img.Records["spc-cache.json"],
		[]byte(`"version": "dev-main+8a10385"`), []byte(`"version": "dev-main+81c2f0e"`), 1)
}

// xcaddyMoved is a carrier built by an xcaddy that moved a commit past
// v0.4.7 on its master branch.
func xcaddyMoved(img *manifesttest.Image) {
	img.Records["xcaddy.txt"] = bytes.Replace(img.Records["xcaddy.txt"],
		[]byte("xcaddy\tv0.4.7\t"), []byte("xcaddy\tv0.4.8-0.20261112093000-0123456789ab\t"), 1)
}

// unrecorded is a carrier from before the build recorded xcaddy.
func unrecorded(img *manifesttest.Image) {
	delete(img.Records, "xcaddy.txt")
}

func capability(img *manifesttest.Image) {
	img.BinaryPAX = map[string]string{manifesttest.CapabilityPAX: "\x01"}
}

func writeJSON(t *testing.T, m *manifest.Manifest) string {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFile(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunOCI(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		args       func(f fixture) []string
		wantCode   int
		wantStderr string
	}{
		{
			name: "ok",
			args: func(f fixture) []string {
				return []string{"-oci", f.carrier("baikal", "amd64", nil), "-arch", "amd64"}
			},
			wantCode: 0,
		},
		{
			name: "ok-with-app",
			args: func(f fixture) []string {
				return []string{"-oci", f.carrier("baikal", "amd64", nil), "-arch", "amd64", "-app", "baikal"}
			},
			wantCode: 0,
		},
		{
			name: "capability",
			args: func(f fixture) []string {
				return []string{"-oci", f.carrier("baikal", "arm64", capability), "-arch", "arm64", "-app", "baikal"}
			},
			wantCode: 1, wantStderr: "file capability",
		},
		{
			name: "another-app",
			args: func(f fixture) []string {
				return []string{"-oci", f.carrier("librespeed", "arm64", nil), "-arch", "arm64", "-app", "baikal"}
			},
			wantCode: 1, wantStderr: "librespeed",
		},
		{
			name: "another-arch",
			args: func(f fixture) []string {
				return []string{"-oci", f.carrier("baikal", "arm64", nil), "-arch", "amd64"}
			},
			wantCode: 1, wantStderr: "linux/amd64",
		},
		{
			name:     "no-arch",
			args:     func(f fixture) []string { return []string{"-oci", f.carrier("baikal", "arm64", nil)} },
			wantCode: 2, wantStderr: "-arch",
		},
		{
			name: "stray-argument",
			args: func(f fixture) []string {
				return []string{"-oci", f.carrier("baikal", "arm64", nil), "-arch", "arm64", "extra"}
			},
			wantCode: 2, wantStderr: "extra",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := run(tt.args(fixture{t}), &stdout, &stderr)
			if code != tt.wantCode {
				t.Fatalf("run() = %d, want %d; stderr: %s", code, tt.wantCode, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to hold %q", stderr.String(), tt.wantStderr)
			}
			if code != 0 {
				if stdout.Len() != 0 {
					t.Errorf("stdout = %q, want nothing", stdout.String())
				}
				return
			}
			var m manifest.Manifest
			dec := json.NewDecoder(&stdout)
			dec.DisallowUnknownFields()
			if err := dec.Decode(&m); err != nil {
				t.Fatalf("stdout is not a manifest: %v", err)
			}
			if m.App != "baikal" || m.Arch != "amd64" || len(m.Libraries) != 9 {
				t.Errorf("printed %s/%s with %d libraries, want baikal/amd64 with 9", m.App, m.Arch, len(m.Libraries))
			}
		})
	}
}

func TestRunCompare(t *testing.T) {
	t.Parallel()
	base, err := manifest.FromOCIArchive(carrier(t, "librespeed", "arm64", nil), "arm64")
	if err != nil {
		t.Fatal(err)
	}
	bumped := base
	bumped.PHP = "8.5.13"

	tests := []struct {
		name       string
		args       func(f fixture) []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name:     "equal",
			args:     func(f fixture) []string { return []string{"-compare", f.json(&base), f.json(&base)} },
			wantCode: 0,
		},
		{
			name:       "differ",
			args:       func(f fixture) []string { return []string{"-compare", f.json(&base), f.json(&bumped)} },
			wantCode:   1,
			wantStdout: "php: 8.5.12 → 8.5.13\n",
		},
		{
			name:       "unreadable",
			args:       func(f fixture) []string { return []string{"-compare", f.json(&base), "/nonexistent/b.json"} },
			wantCode:   2,
			wantStderr: "b.json",
		},
		{
			name: "not-a-manifest",
			args: func(f fixture) []string {
				return []string{"-compare", f.json(&base), f.file(`{"schema":1,"surprise":true}`)}
			},
			wantCode:   2,
			wantStderr: "surprise",
		},
		{
			name:       "one-file",
			args:       func(f fixture) []string { return []string{"-compare", f.json(&base)} },
			wantCode:   2,
			wantStderr: "two",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := run(tt.args(fixture{t}), &stdout, &stderr)
			if code != tt.wantCode {
				t.Fatalf("run() = %d, want %d; stderr: %s", code, tt.wantCode, stderr.String())
			}
			if stdout.String() != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.wantStdout)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to hold %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func TestRunChanges(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		args       func(f fixture) []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name: "nothing-moved",
			args: func(f fixture) []string {
				return []string{
					"-changes", "-app", "librespeed", "-published", f.published("librespeed", nil, "amd64", "arm64"),
					"amd64=" + f.carrier("librespeed", "amd64", nil), "arm64=" + f.carrier("librespeed", "arm64", nil),
				}
			},
			wantCode: 0,
		},
		{
			name: "arm64-moved",
			args: func(f fixture) []string {
				return []string{
					"-changes", "-app", "librespeed", "-published", f.published("librespeed", nil, "amd64", "arm64"),
					"amd64=" + f.carrier("librespeed", "amd64", nil), "arm64=" + f.carrier("librespeed", "arm64", moved),
				}
			},
			wantCode:   0,
			wantStdout: "arm64: library libaom: dev-main+8a10385@8a1038536cde81c548341145f40d6dfddc3077d6 → dev-main+81c2f0e@8a1038536cde81c548341145f40d6dfddc3077d6\n",
		},
		{
			// Both architectures, as every build of a day installs the
			// same xcaddy
			name: "xcaddy-moved",
			args: func(f fixture) []string {
				return []string{
					"-changes", "-app", "librespeed", "-published", f.published("librespeed", nil, "amd64", "arm64"),
					"amd64=" + f.carrier("librespeed", "amd64", xcaddyMoved), "arm64=" + f.carrier("librespeed", "arm64", xcaddyMoved),
				}
			},
			wantCode: 0,
			wantStdout: "amd64: xcaddy: v0.4.7 → v0.4.8-0.20261112093000-0123456789ab\n" +
				"arm64: xcaddy: v0.4.7 → v0.4.8-0.20261112093000-0123456789ab\n",
		},
		{
			// Refused, as any published carrier missing a record is: the
			// daily run compares only a published image built from main's
			// own commit, and every one built since xcaddy is recorded
			// holds it.
			name: "published-without-xcaddy",
			args: func(f fixture) []string {
				return []string{
					"-changes", "-app", "librespeed", "-published", f.published("librespeed", unrecorded, "amd64", "arm64"),
					"amd64=" + f.carrier("librespeed", "amd64", nil), "arm64=" + f.carrier("librespeed", "arm64", nil),
				}
			},
			wantCode:   1,
			wantStderr: "xcaddy.txt",
		},
		{
			name: "amd64-unpublished",
			args: func(f fixture) []string {
				return []string{
					"-changes", "-app", "librespeed", "-published", f.published("librespeed", nil, "arm64"),
					"amd64=" + f.carrier("librespeed", "amd64", nil), "arm64=" + f.carrier("librespeed", "arm64", nil),
				}
			},
			wantCode:   0,
			wantStdout: "amd64: not published\n",
		},
		{
			name: "nothing-published",
			args: func(f fixture) []string {
				return []string{
					"-changes", "-app", "baikal",
					"amd64=" + f.carrier("baikal", "amd64", nil), "arm64=" + f.carrier("baikal", "arm64", nil),
				}
			},
			wantCode:   0,
			wantStdout: "amd64: not published\narm64: not published\n",
		},
		{
			name: "arm64-missing",
			args: func(f fixture) []string {
				return []string{
					"-changes", "-app", "librespeed", "-published", f.published("librespeed", nil, "amd64", "arm64"),
					"amd64=" + f.carrier("librespeed", "amd64", nil),
				}
			},
			wantCode:   1,
			wantStderr: "arm64",
		},
		{
			name: "candidate-capability",
			args: func(f fixture) []string {
				return []string{
					"-changes", "-app", "librespeed", "-published", f.published("librespeed", nil, "amd64", "arm64"),
					"amd64=" + f.carrier("librespeed", "amd64", capability), "arm64=" + f.carrier("librespeed", "arm64", nil),
				}
			},
			wantCode:   1,
			wantStderr: "file capability",
		},
		{
			name: "candidate-another-app",
			args: func(f fixture) []string {
				return []string{
					"-changes", "-app", "librespeed", "-published", f.published("librespeed", nil, "amd64", "arm64"),
					"amd64=" + f.carrier("librespeed", "amd64", nil), "arm64=" + f.carrier("baikal", "arm64", nil),
				}
			},
			wantCode:   1,
			wantStderr: "baikal",
		},
		{
			name: "published-another-app",
			args: func(f fixture) []string {
				return []string{
					"-changes", "-app", "librespeed", "-published", f.published("baikal", nil, "amd64", "arm64"),
					"amd64=" + f.carrier("librespeed", "amd64", nil), "arm64=" + f.carrier("librespeed", "arm64", nil),
				}
			},
			wantCode:   1,
			wantStderr: "baikal",
		},
		{
			name: "published-unreadable",
			args: func(f fixture) []string {
				return []string{
					"-changes", "-app", "librespeed", "-published", f.file("junk"),
					"amd64=" + f.carrier("librespeed", "amd64", nil), "arm64=" + f.carrier("librespeed", "arm64", nil),
				}
			},
			wantCode:   1,
			wantStderr: "index.json",
		},
		{
			name:       "no-app",
			args:       func(fixture) []string { return []string{"-changes", "amd64=a.tar", "arm64=b.tar"} },
			wantCode:   2,
			wantStderr: "-app",
		},
		{
			name:       "not-arch-equals-archive",
			args:       func(fixture) []string { return []string{"-changes", "-app", "baikal", "a.tar"} },
			wantCode:   2,
			wantStderr: "a.tar",
		},
		{
			name:       "arch-twice",
			args:       func(fixture) []string { return []string{"-changes", "-app", "baikal", "amd64=a.tar", "amd64=b.tar"} },
			wantCode:   2,
			wantStderr: "amd64",
		},
		{
			name:       "no-mode",
			args:       func(fixture) []string { return []string{"-app", "baikal"} },
			wantCode:   2,
			wantStderr: "usage",
		},
		{
			name:       "two-modes",
			args:       func(fixture) []string { return []string{"-changes", "-compare", "-app", "baikal"} },
			wantCode:   2,
			wantStderr: "usage",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := run(tt.args(fixture{t}), &stdout, &stderr)
			if code != tt.wantCode {
				t.Fatalf("run() = %d, want %d; stderr: %s", code, tt.wantCode, stderr.String())
			}
			if stdout.String() != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.wantStdout)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to hold %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

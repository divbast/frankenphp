// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package manifest_test

import (
	"bytes"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/divbast/frankenphp/internal/manifest"
	"github.com/divbast/frankenphp/internal/manifest/manifesttest"
)

// The records in testdata/ are those of two local arm64 builds of the
// carriers, one per app, as records.sh wrote them.

var libreSpeedLibraries = map[string]string{
	"brotli":   "v1.2.1@59725b2fe394af2e8b5d654ef1d432d9714f6689",
	"bzip2":    "bzip2-1.0.8.tar.gz@bf7badf7e248e0ecf465d33c2f5aeec774209227",
	"freetype": "VER-2-15-0@283cc555b95af726fe4f9730fd03227a469f8626",
	"libaom":   "dev-main+8a10385@8a1038536cde81c548341145f40d6dfddc3077d6",
	"libavif":  "v1.4.2@8a376a6a66b58c8b95085a3ee70af4794d517d4e",
	"libiconv": "1.19.1@3f350c71067c4854cf03fac81ccd749b0be988b0",
	"libjpeg":  "3.2.1@0de9bda1fb7229d5c01d423527538b0b710ab0ad",
	"libpng":   "v1.6.59@b9717eb8e4743719ed2d8e5360345da91f1273a8",
	"libwebp":  "v1.6.0@d095c922bb7a99acc2b0a7da9607e654d9b55a70",
	"libxml2":  "v2.15.4@2bdd4831130636714412b93b78fecb30f0e00f6f",
	"openssl":  "openssl-3.6.5@89256e2bf2e5c761c9b4c49925d816f0e3b25fe4",
	"sqlite":   "sqlite-autoconf-3450200.tar.gz@ad56cfe252ea0a86c22812c79f207d265ae4533d",
	"watcher":  "0.14.5@89ba560e26de9b107de9aede4ab60592ec46618d",
	"xz":       "v5.8.4@b1345221051ef5ac65297926418b56d2b941fe64",
	"zlib":     "v1.4@6bb95b58fb638ec49d921557bf66183baa4c6522",
}

var baikalLibraries = map[string]string{
	"brotli":   "v1.2.1@59725b2fe394af2e8b5d654ef1d432d9714f6689",
	"libiconv": "1.19.1@3f350c71067c4854cf03fac81ccd749b0be988b0",
	"libxml2":  "v2.15.4@2bdd4831130636714412b93b78fecb30f0e00f6f",
	"onig":     "v6.9.10@bcada943679fa8cecfe359e57d785da9430dcdbc",
	"openssl":  "openssl-3.6.5@89256e2bf2e5c761c9b4c49925d816f0e3b25fe4",
	"sqlite":   "sqlite-autoconf-3450200.tar.gz@ad56cfe252ea0a86c22812c79f207d265ae4533d",
	"watcher":  "0.14.5@89ba560e26de9b107de9aede4ab60592ec46618d",
	"xz":       "v5.8.4@b1345221051ef5ac65297926418b56d2b941fe64",
	"zlib":     "v1.4@6bb95b58fb638ec49d921557bf66183baa4c6522",
}

var libreSpeedExtensions = []string{
	"Core", "PDO", "Phar", "Reflection", "SPL", "Zend OPcache", "ctype", "date",
	"filter", "frankenphp-cli", "gd", "hash", "json", "lexbor", "openssl", "pcntl",
	"pcre", "pdo_sqlite", "posix", "random", "session", "sqlite3", "standard",
	"uri", "zlib",
}

// records reads one app's records from testdata.
func records(t *testing.T, app string) map[string][]byte {
	t.Helper()
	dir := filepath.Join("testdata", app)
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
	return files
}

// rootOf writes files into a temporary directory and opens it as a root.
func rootOf(t *testing.T, files map[string][]byte) *os.Root {
	t.Helper()
	dir := t.TempDir()
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	return root
}

// edited returns a copy of files with old replaced by new in the named
// file, which must hold old exactly once.
func edited(t *testing.T, files map[string][]byte, name, old, replacement string) map[string][]byte {
	t.Helper()
	if n := bytes.Count(files[name], []byte(old)); n != 1 {
		t.Fatalf("%s holds %q %d times, not once", name, old, n)
	}
	out := maps.Clone(files)
	out[name] = bytes.Replace(files[name], []byte(old), []byte(replacement), 1)
	return out
}

// retyped returns a copy of files whose build manifest names every library
// a tool, so that it lists none.
func retyped(files map[string][]byte) map[string][]byte {
	out := maps.Clone(files)
	out["build-manifest.json"] = bytes.ReplaceAll(files["build-manifest.json"], []byte(`"type": "library"`), []byte(`"type": "tool"`))
	return out
}

func without(files map[string][]byte, name string) map[string][]byte {
	out := maps.Clone(files)
	delete(out, name)
	return out
}

// fromRecords is the manifest of one app's testdata records.
func fromRecords(t *testing.T, app string) manifest.Manifest {
	t.Helper()
	m, err := manifest.FromRecords(rootOf(t, records(t, app)))
	if err != nil {
		t.Fatal(err)
	}
	// The tests built on this one edit its maps.
	if len(m.Libraries) == 0 || len(m.GoModules) == 0 {
		t.Fatalf("FromRecords() of %s's records lists no libraries or Go modules", app)
	}
	return m
}

func clone(orig *manifest.Manifest) manifest.Manifest {
	m := *orig
	m.Extensions = slices.Clone(m.Extensions)
	m.Libraries = maps.Clone(m.Libraries)
	m.GoModules = maps.Clone(m.GoModules)
	return m
}

func TestFromRecords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		app            string
		libraries      map[string]string
		extensions     int
		wantExtensions []string
	}{
		{app: "librespeed", libraries: libreSpeedLibraries, extensions: 25, wantExtensions: libreSpeedExtensions},
		{app: "baikal", libraries: baikalLibraries, extensions: 32},
	}
	for _, tt := range tests {
		t.Run(tt.app, func(t *testing.T) {
			t.Parallel()
			got := fromRecords(t, tt.app)

			scalars := manifest.Manifest{
				Schema: got.Schema, App: got.App, Arch: got.Arch,
				FrankenPHP: got.FrankenPHP, PHP: got.PHP, Caddy: got.Caddy, Go: got.Go,
			}
			want := manifest.Manifest{
				Schema: 1, App: tt.app, Arch: "arm64",
				FrankenPHP: "v1.13.5", PHP: "8.5.12", Caddy: "v2.11.9", Go: "go1.27.4",
			}
			if !reflect.DeepEqual(scalars, want) {
				t.Errorf("FromRecords() = %+v, want %+v", scalars, want)
			}
			if !maps.Equal(got.Libraries, tt.libraries) {
				t.Errorf("Libraries = %v, want %v", got.Libraries, tt.libraries)
			}
			if len(got.Libraries) != len(tt.libraries) {
				t.Errorf("%d libraries, want %d", len(got.Libraries), len(tt.libraries))
			}
			if len(got.Extensions) != tt.extensions || !slices.IsSorted(got.Extensions) {
				t.Errorf("Extensions = %q, want %d, sorted", got.Extensions, tt.extensions)
			}
			if tt.wantExtensions != nil && !slices.Equal(got.Extensions, tt.wantExtensions) {
				t.Errorf("Extensions = %q, want %q", got.Extensions, tt.wantExtensions)
			}
			if len(got.GoModules) != 176 {
				t.Errorf("%d Go modules, want 176", len(got.GoModules))
			}
			for module, version := range map[string]string{
				"github.com/caddyserver/caddy/v2": "v2.11.9",
				"github.com/dunglas/frankenphp":   "v1.13.5 => /go/src/app (devel)",
				"golang.org/x/net":                got.GoModules["golang.org/x/net"],
			} {
				if got.GoModules[module] != version || version == "" {
					t.Errorf("GoModules[%q] = %q, want %q", module, got.GoModules[module], version)
				}
			}
		})
	}
}

func TestFromRecordsSortsExtensions(t *testing.T) {
	t.Parallel()
	files := records(t, "librespeed")
	lines := strings.Split(strings.TrimSpace(string(files["extensions.txt"])), "\n")
	slices.Reverse(lines)
	files["extensions.txt"] = []byte(strings.Join(lines, "\n") + "\n")
	got, err := manifest.FromRecords(rootOf(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Extensions, libreSpeedExtensions) {
		t.Errorf("Extensions = %q, want %q", got.Extensions, libreSpeedExtensions)
	}
}

func TestFromRecordsRefuses(t *testing.T) {
	t.Parallel()
	ls := records(t, "librespeed")

	tests := []struct {
		name  string
		files map[string][]byte
		// want is a substring the error must hold.
		want string
		// wantIs is an error the error must wrap, if any.
		wantIs error
	}{
		{name: "no-app", files: without(ls, "app.txt"), want: "app.txt", wantIs: fs.ErrNotExist},
		{name: "no-version", files: without(ls, "version.txt"), want: "version.txt", wantIs: fs.ErrNotExist},
		{name: "no-build-info", files: without(ls, "build-info.txt"), want: "build-info.txt", wantIs: fs.ErrNotExist},
		{name: "no-extensions", files: without(ls, "extensions.txt"), want: "extensions.txt", wantIs: fs.ErrNotExist},
		{name: "no-spc-cache", files: without(ls, "spc-cache.json"), want: "spc-cache.json", wantIs: fs.ErrNotExist},
		{name: "no-build-manifest", files: without(ls, "build-manifest.json"), want: "build-manifest.json", wantIs: fs.ErrNotExist},
		{name: "empty-app", files: edited(t, ls, "app.txt", "librespeed", ""), want: "app.txt"},
		{name: "two-word-app", files: edited(t, ls, "app.txt", "librespeed", "libre speed"), want: "app.txt"},
		{name: "version-garbled", files: edited(t, ls, "version.txt", "PHP 8.5.12", "PHP"), want: "version.txt"},
		{name: "no-go-line", files: edited(t, ls, "build-info.txt", "go\tgo1.27.4\n", ""), want: "build-info.txt"},
		{name: "no-goarch", files: edited(t, ls, "build-info.txt", "build\tGOARCH=arm64\n", ""), want: "GOARCH"},
		{name: "replace-without-dep", files: edited(t, ls, "build-info.txt", "path\tcaddy\n", "=>\t/x\t(devel)\n"), want: "build-info.txt"},
		{name: "no-extensions-listed", files: map[string][]byte{
			"app.txt": ls["app.txt"], "version.txt": ls["version.txt"], "build-info.txt": ls["build-info.txt"],
			"extensions.txt": []byte("\n"), "spc-cache.json": ls["spc-cache.json"], "build-manifest.json": ls["build-manifest.json"],
		}, want: "extensions.txt"},
		{name: "spc-cache-garbled", files: edited(t, ls, "spc-cache.json", `"libwebp": {`, `"libwebp": [`), want: "spc-cache.json"},
		{name: "build-manifest-schema", files: edited(t, ls, "build-manifest.json", `"schema-version": 1`, `"schema-version": 2`), want: "schema-version"},
		{name: "library-uncached", files: edited(t, ls, "spc-cache.json", `"libaom": {`, `"libaom-gone": {`), want: "libaom"},
		{name: "library-unversioned", files: edited(t, ls, "spc-cache.json", `"version": "dev-main+8a10385"`, `"version": null`), want: "libaom"},
		{name: "library-unhashed", files: edited(t, ls, "spc-cache.json", `"hash": "8a1038536cde81c548341145f40d6dfddc3077d6"`, `"hash": null`), want: "libaom"},
		{name: "no-libraries", files: retyped(ls), want: "no library"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := manifest.FromRecords(rootOf(t, tt.files))
			if err == nil {
				t.Fatalf("FromRecords() succeeded, want an error holding %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("FromRecords() error = %q, want it to hold %q", err, tt.want)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("FromRecords() error = %v, want one wrapping %v", err, tt.wantIs)
			}
		})
	}
}

// asArch returns records whose build info names arch, as an amd64 build's
// would.
func asArch(t *testing.T, files map[string][]byte, arch string) map[string][]byte {
	t.Helper()
	if arch == "arm64" {
		return files
	}
	return edited(t, files, "build-info.txt", "GOARCH=arm64", "GOARCH="+arch)
}

func TestFromOCIArchive(t *testing.T) {
	t.Parallel()
	ls := records(t, "librespeed")
	want := fromRecords(t, "librespeed")
	wantAMD64 := clone(&want)
	wantAMD64.Arch = "amd64"

	published := manifesttest.Archive{Images: []manifesttest.Image{
		{Arch: "amd64", Records: asArch(t, ls, "amd64")},
		{Arch: "arm64", Records: ls},
	}}

	tests := []struct {
		name    string
		archive manifesttest.Archive
		arch    string
		want    manifest.Manifest
	}{
		{
			name:    "build",
			archive: manifesttest.Archive{Images: []manifesttest.Image{{Arch: "arm64", Records: ls}}},
			arch:    "arm64",
			want:    want,
		},
		{name: "published-arm64", archive: published, arch: "arm64", want: want},
		{name: "published-amd64", archive: published, arch: "amd64", want: wantAMD64},
		{
			name:    "flat",
			archive: manifesttest.Archive{Flat: true, Images: []manifesttest.Image{{Arch: "amd64", Records: asArch(t, ls, "amd64")}}},
			arch:    "amd64",
			want:    wantAMD64,
		},
		{
			name:    "uncompressed",
			archive: manifesttest.Archive{Images: []manifesttest.Image{{Arch: "arm64", Records: ls, LayerMediaType: manifesttest.MediaLayerTar}}},
			arch:    "arm64",
			want:    want,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := manifest.FromOCIArchive(manifesttest.Write(t, tt.archive), tt.arch)
			if err != nil {
				t.Fatalf("FromOCIArchive() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FromOCIArchive() differs from the records: %q", manifest.Diff(tt.want, got))
			}
		})
	}
}

func TestFromOCIArchiveRefuses(t *testing.T) {
	t.Parallel()
	ls := records(t, "librespeed")
	one := func(img manifesttest.Image) manifesttest.Archive {
		if img.Arch == "" {
			img.Arch = "arm64"
		}
		if img.Records == nil {
			img.Records = ls
		}
		return manifesttest.Archive{Images: []manifesttest.Image{img}}
	}

	tests := []struct {
		name    string
		archive *manifesttest.Archive
		// path replaces the archive with a file holding these bytes.
		path []byte
		arch string
		// want is a substring the error must hold.
		want string
		// wantIs is an error the error must wrap, if any.
		wantIs error
	}{
		{name: "arch-absent", archive: new(one(manifesttest.Image{})), arch: "amd64", want: "amd64", wantIs: manifest.ErrNoImage},
		{
			name:    "capability",
			archive: new(one(manifesttest.Image{BinaryPAX: map[string]string{manifesttest.CapabilityPAX: "\x01\x00\x00\x02\x00\x04\x00\x00"}})),
			arch:    "arm64", want: "frankenphp", wantIs: manifest.ErrFileCapability,
		},
		{name: "no-binary", archive: new(one(manifesttest.Image{NoBinary: true})), arch: "arm64", want: "/frankenphp"},
		{name: "records-missing", archive: new(one(manifesttest.Image{Records: without(ls, "build-manifest.json")})), arch: "arm64", want: "build-manifest.json", wantIs: fs.ErrNotExist},
		{name: "goarch-disagrees", archive: new(one(manifesttest.Image{Arch: "amd64"})), arch: "amd64", want: "arm64", wantIs: manifest.ErrArch},
		{name: "config-disagrees", archive: new(one(manifesttest.Image{ConfigArch: "amd64"})), arch: "arm64", want: "amd64", wantIs: manifest.ErrArch},
		{name: "whiteout", archive: new(one(manifesttest.Image{Extra: []manifesttest.Entry{{Name: "records/.wh.app.txt"}}})), arch: "arm64", want: ".wh."},
		{name: "zstd", archive: new(one(manifesttest.Image{LayerMediaType: manifesttest.MediaLayerZstd})), arch: "arm64", want: "zstd"},
		{
			name: "two-images",
			archive: &manifesttest.Archive{Images: []manifesttest.Image{
				{Arch: "arm64", Records: ls}, {Arch: "arm64", Records: edited(t, ls, "app.txt", "librespeed", "baikal")},
			}},
			arch: "arm64", want: "2 images",
		},
		{name: "not-a-tar", path: []byte("this is not an archive\n"), arch: "arm64", want: "index.json"},
		{name: "absent", arch: "arm64", want: "nothing-here.tar", wantIs: fs.ErrNotExist},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "nothing-here.tar")
			switch {
			case tt.archive != nil:
				path = manifesttest.Write(t, *tt.archive)
			case tt.path != nil:
				if err := os.WriteFile(path, tt.path, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := manifest.FromOCIArchive(path, tt.arch)
			if err == nil {
				t.Fatalf("FromOCIArchive() succeeded, want an error holding %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("FromOCIArchive() error = %q, want it to hold %q", err, tt.want)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("FromOCIArchive() error = %v, want one wrapping %v", err, tt.wantIs)
			}
			for _, other := range []error{manifest.ErrNoImage, manifest.ErrFileCapability, manifest.ErrArch} {
				if !errors.Is(other, tt.wantIs) && errors.Is(err, other) {
					t.Errorf("FromOCIArchive() error = %v wraps %v as well", err, other)
				}
			}
		})
	}
}

const (
	aomOld = "dev-main+8a10385@8a1038536cde81c548341145f40d6dfddc3077d6"
	aomNew = "dev-main+81c2f0e@81c2f0e4b0c0d3a5a1e7f9b2c6d8e0a1b3c5d7f9"
)

func TestDiff(t *testing.T) {
	t.Parallel()
	base := fromRecords(t, "librespeed")

	tests := []struct {
		name string
		edit func(m *manifest.Manifest)
		want []string
	}{
		{name: "equal", edit: func(*manifest.Manifest) {}, want: nil},
		{
			name: "libaom-moved",
			edit: func(m *manifest.Manifest) { m.Libraries["libaom"] = aomNew },
			want: []string{"library libaom: " + aomOld + " → " + aomNew},
		},
		{
			name: "extension-added",
			edit: func(m *manifest.Manifest) {
				m.Extensions = slices.Sorted(slices.Values(append(m.Extensions, "mbstring")))
			},
			want: []string{"extension mbstring: added"},
		},
		{
			name: "extension-removed",
			edit: func(m *manifest.Manifest) {
				m.Extensions = slices.DeleteFunc(m.Extensions, func(e string) bool { return e == "gd" })
			},
			want: []string{"extension gd: removed"},
		},
		{
			name: "go-module-bumped",
			edit: func(m *manifest.Manifest) { m.GoModules["github.com/caddyserver/caddy/v2"] = "v2.11.10" },
			want: []string{"go module github.com/caddyserver/caddy/v2: v2.11.9 → v2.11.10"},
		},
		{
			name: "library-added",
			edit: func(m *manifest.Manifest) { m.Libraries["onig"] = "v6.9.10@bcada943679fa8cecfe359e57d785da9430dcdbc" },
			want: []string{"library onig: added at v6.9.10@bcada943679fa8cecfe359e57d785da9430dcdbc"},
		},
		{
			name: "library-removed",
			edit: func(m *manifest.Manifest) { delete(m.Libraries, "bzip2") },
			want: []string{"library bzip2: removed, was bzip2-1.0.8.tar.gz@bf7badf7e248e0ecf465d33c2f5aeec774209227"},
		},
		{
			name: "everything-in-order",
			edit: func(m *manifest.Manifest) {
				m.GoModules["golang.org/x/net"] = "v9.9.9"
				m.Libraries["libaom"] = aomNew
				m.Go = "go1.27.5"
				m.PHP = "8.5.13"
				m.Extensions = slices.DeleteFunc(m.Extensions, func(e string) bool { return e == "Phar" })
			},
			want: []string{
				"php: 8.5.12 → 8.5.13",
				"go: go1.27.4 → go1.27.5",
				"extension Phar: removed",
				"library libaom: " + aomOld + " → " + aomNew,
				"go module golang.org/x/net: " + base.GoModules["golang.org/x/net"] + " → v9.9.9",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := clone(&base)
			tt.edit(&b)
			if got := manifest.Diff(base, b); !slices.Equal(got, tt.want) {
				t.Errorf("Diff() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChanges(t *testing.T) {
	t.Parallel()
	arm := fromRecords(t, "librespeed")
	amd := clone(&arm)
	amd.Arch = "amd64"
	armMoved := clone(&arm)
	armMoved.Libraries["libaom"] = aomNew
	amdMoved := clone(&amd)
	amdMoved.PHP = "8.5.13"

	tests := []struct {
		name    string
		pairs   map[string]manifest.Pair
		want    string
		wantErr string
	}{
		{
			name: "one-arch-moved",
			pairs: map[string]manifest.Pair{
				"amd64": {Candidate: amd, Published: new(amd)},
				"arm64": {Candidate: armMoved, Published: new(arm)},
			},
			want: "arm64: library libaom: " + aomOld + " → " + aomNew,
		},
		{
			name: "both-equal",
			pairs: map[string]manifest.Pair{
				"amd64": {Candidate: amd, Published: new(amd)},
				"arm64": {Candidate: arm, Published: new(arm)},
			},
			want: "",
		},
		{
			name: "both-moved",
			pairs: map[string]manifest.Pair{
				"amd64": {Candidate: amdMoved, Published: new(amd)},
				"arm64": {Candidate: armMoved, Published: new(arm)},
			},
			want: "amd64: php: 8.5.12 → 8.5.13\narm64: library libaom: " + aomOld + " → " + aomNew,
		},
		{
			name: "one-arch-unpublished",
			pairs: map[string]manifest.Pair{
				"amd64": {Candidate: amd},
				"arm64": {Candidate: arm, Published: new(arm)},
			},
			want: "amd64: not published",
		},
		{
			name:    "arm64-missing",
			pairs:   map[string]manifest.Pair{"amd64": {Candidate: amd, Published: new(amd)}},
			wantErr: "arm64",
		},
		{
			name:    "amd64-missing",
			pairs:   map[string]manifest.Pair{"arm64": {Candidate: arm, Published: new(arm)}},
			wantErr: "amd64",
		},
		{
			name: "unknown-arch",
			pairs: map[string]manifest.Pair{
				"amd64":   {Candidate: amd, Published: new(amd)},
				"arm64":   {Candidate: arm, Published: new(arm)},
				"riscv64": {Candidate: arm},
			},
			wantErr: "riscv64",
		},
		{
			name: "candidate-for-another-arch",
			pairs: map[string]manifest.Pair{
				"amd64": {Candidate: arm, Published: new(amd)},
				"arm64": {Candidate: arm, Published: new(arm)},
			},
			wantErr: "amd64",
		},
		{
			name: "published-for-another-arch",
			pairs: map[string]manifest.Pair{
				"amd64": {Candidate: amd, Published: new(amd)},
				"arm64": {Candidate: arm, Published: new(amd)},
			},
			wantErr: "arm64",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := manifest.Changes(tt.pairs)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Changes() = %q, %v; want an error holding %q", got, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Changes() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Changes() = %q, want %q", got, tt.want)
			}
		})
	}
}

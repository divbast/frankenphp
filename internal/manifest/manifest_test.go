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
// carriers, one per app, as records.sh wrote them. xcaddy.txt, which
// records.sh did not write then, is `go version -m` of the xcaddy in each
// app's build stage from a later local build of the same sources, as
// records.sh writes it now. spc.txt, builder.txt, craft.yml and recipe.txt,
// which it wrote later still, are from a schema 3 build of each app.

// The schema 3 inputs every app's testdata records: the Containerfile's
// SPC_COMMIT and BUILDER_IMAGE digest.
const (
	spcCommit     = "3ba9ffa0593b94202b48d6dde51e489191ecdbf1"
	builderDigest = "sha256:4dcdd53971ba1f373c3eb90e393dd3944687938c5c03494ab684921594905fc7"
)

// schema3Records are the records a build adds for schema 3, which every
// carrier built before it lacks.
var schema3Records = []string{"spc.txt", "builder.txt", "craft.yml", "recipe.txt"}

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

// without returns a copy of files without the records named.
func without(files map[string][]byte, name string, more ...string) map[string][]byte {
	out := maps.Clone(files)
	delete(out, name)
	for _, n := range more {
		delete(out, n)
	}
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
		// craft is `sha256sum craft.yml` of the app's testdata, and recipe
		// its recipe.txt.
		craft, recipe string
	}{
		{
			app: "librespeed", libraries: libreSpeedLibraries, extensions: 25, wantExtensions: libreSpeedExtensions,
			craft:  "4f6e7ebe03e0f498f5841813512d9fb01812d80fb0ee982839fc408cf09282a6",
			recipe: "06b287fccf41e26f35a16f2daef3663306b271f7768aacb52cf15840fc60530f",
		},
		{
			app: "baikal", libraries: baikalLibraries, extensions: 32,
			craft:  "d67ca33f36649ffcd110e1414b5e43f8e0613e350020d40c70fec0d75f539c40",
			recipe: "9de52eca29d8aea6e53de0a717d2f8bcb4d64874399ad6351f7c0a7bc889440e",
		},
	}
	for _, tt := range tests {
		t.Run(tt.app, func(t *testing.T) {
			t.Parallel()
			got := fromRecords(t, tt.app)

			scalars := manifest.Manifest{
				Schema: got.Schema, App: got.App, Arch: got.Arch,
				FrankenPHP: got.FrankenPHP, PHP: got.PHP, Caddy: got.Caddy, Go: got.Go, Xcaddy: got.Xcaddy,
				SPC: got.SPC, Builder: got.Builder, Craft: got.Craft, Recipe: got.Recipe,
			}
			want := manifest.Manifest{
				Schema: 3, App: tt.app, Arch: "arm64",
				FrankenPHP: "v1.13.5", PHP: "8.5.12", Caddy: "v2.11.9", Go: "go1.27.4", Xcaddy: "v0.4.7",
				SPC: spcCommit, Builder: builderDigest, Craft: tt.craft, Recipe: tt.recipe,
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
	// Every record there, and one this commit cannot read
	const full = manifest.SchemaVersion

	tests := []struct {
		name  string
		files map[string][]byte
		// want is a substring the error must hold.
		want string
		// wantIs is an error the error must wrap, if any.
		wantIs error
		// schema is the UnreadableError's Schema: the newest schema whose
		// records are all there.
		schema int
	}{
		{name: "no-app", files: without(ls, "app.txt"), want: "app.txt", wantIs: fs.ErrNotExist},
		{name: "no-version", files: without(ls, "version.txt"), want: "version.txt", wantIs: fs.ErrNotExist},
		{name: "no-build-info", files: without(ls, "build-info.txt"), want: "build-info.txt", wantIs: fs.ErrNotExist},
		{name: "no-extensions", files: without(ls, "extensions.txt"), want: "extensions.txt", wantIs: fs.ErrNotExist},
		{name: "no-spc-cache", files: without(ls, "spc-cache.json"), want: "spc-cache.json", wantIs: fs.ErrNotExist},
		{name: "no-build-manifest", files: without(ls, "build-manifest.json"), want: "build-manifest.json", wantIs: fs.ErrNotExist},
		// As every carrier holds that was built before xcaddy was recorded
		{name: "no-xcaddy", files: without(ls, "xcaddy.txt", schema3Records...), want: "xcaddy.txt", wantIs: fs.ErrNotExist, schema: 1},
		// As every carrier holds that was built before schema 3
		{name: "schema-2", files: without(ls, schema3Records[0], schema3Records[1:]...), want: "spc.txt", wantIs: fs.ErrNotExist, schema: 2},
		{name: "no-spc", files: without(ls, "spc.txt"), want: "spc.txt", wantIs: fs.ErrNotExist, schema: 2},
		{name: "no-builder", files: without(ls, "builder.txt"), want: "builder.txt", wantIs: fs.ErrNotExist, schema: 2},
		{name: "no-craft", files: without(ls, "craft.yml"), want: "craft.yml", wantIs: fs.ErrNotExist, schema: 2},
		{name: "no-recipe", files: without(ls, "recipe.txt"), want: "recipe.txt", wantIs: fs.ErrNotExist, schema: 2},
		{name: "spc-not-a-commit", files: edited(t, ls, "spc.txt", spcCommit, "v3"), want: "spc.txt", schema: full},
		{name: "builder-without-digest", files: edited(t, ls, "builder.txt", "@"+builderDigest, ""), want: "builder.txt", schema: full},
		{name: "builder-short-digest", files: edited(t, ls, "builder.txt", builderDigest, builderDigest[:20]), want: "builder.txt", schema: full},
		{name: "craft-empty", files: edited(t, ls, "craft.yml", string(ls["craft.yml"]), ""), want: "craft.yml", schema: full},
		{name: "recipe-with-file-name", files: edited(t, ls, "recipe.txt", "\n", "  -\n"), want: "recipe.txt", schema: full},
		{name: "empty-app", files: edited(t, ls, "app.txt", "librespeed", ""), want: "app.txt", schema: full},
		{name: "two-word-app", files: edited(t, ls, "app.txt", "librespeed", "libre speed"), want: "app.txt", schema: full},
		{name: "version-garbled", files: edited(t, ls, "version.txt", "PHP 8.5.12", "PHP"), want: "version.txt", schema: full},
		{name: "no-go-line", files: edited(t, ls, "build-info.txt", "go\tgo1.27.4\n", ""), want: "build-info.txt", schema: full},
		{name: "no-goarch", files: edited(t, ls, "build-info.txt", "build\tGOARCH=arm64\n", ""), want: "GOARCH", schema: full},
		{name: "replace-without-dep", files: edited(t, ls, "build-info.txt", "path\tcaddy\n", "=>\t/x\t(devel)\n"), want: "build-info.txt", schema: full},
		{name: "no-extensions-listed", files: edited(t, ls, "extensions.txt", string(ls["extensions.txt"]), "\n"), want: "extensions.txt", schema: full},
		// What records.sh would write if XCADDY_BIN named another Go binary
		{name: "xcaddy-another-module", files: edited(t, ls, "xcaddy.txt", "mod\tgithub.com/caddyserver/xcaddy\t", "mod\tcaddy\t"), want: "xcaddy.txt", schema: full},
		{name: "xcaddy-no-module", files: edited(t, ls, "xcaddy.txt", "\tmod\tgithub.com/caddyserver/xcaddy\tv0.4.7\th1:WHNm6tqCFXcOxBovXqMzOvNa0W4arbQRxCeNap2S+aU=\n", ""), want: "xcaddy.txt", schema: full},
		// A build from a local checkout, whose version names no commit
		{name: "xcaddy-devel", files: edited(t, ls, "xcaddy.txt", "xcaddy\tv0.4.7\t", "xcaddy\t(devel)\t"), want: "(devel)", schema: full},
		{name: "spc-cache-garbled", files: edited(t, ls, "spc-cache.json", `"libwebp": {`, `"libwebp": [`), want: "spc-cache.json", schema: full},
		{name: "build-manifest-schema", files: edited(t, ls, "build-manifest.json", `"schema-version": 1`, `"schema-version": 2`), want: "schema-version", schema: full},
		{name: "library-uncached", files: edited(t, ls, "spc-cache.json", `"libaom": {`, `"libaom-gone": {`), want: "libaom", schema: full},
		{name: "library-unversioned", files: edited(t, ls, "spc-cache.json", `"version": "dev-main+8a10385"`, `"version": null`), want: "libaom", schema: full},
		{name: "library-unhashed", files: edited(t, ls, "spc-cache.json", `"hash": "8a1038536cde81c548341145f40d6dfddc3077d6"`, `"hash": null`), want: "libaom", schema: full},
		{name: "no-libraries", files: retyped(ls), want: "no library", schema: full},
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
			if ue, ok := errors.AsType[*manifest.UnreadableError](err); !ok {
				t.Errorf("FromRecords() error = %v, want an *UnreadableError", err)
			} else if ue.Schema != tt.schema {
				t.Errorf("UnreadableError.Schema = %d, want %d", ue.Schema, tt.schema)
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
		// unreadable is whether the error must wrap an *UnreadableError,
		// as only records that cannot be read do.
		unreadable bool
	}{
		{name: "arch-absent", archive: new(one(manifesttest.Image{})), arch: "amd64", want: "amd64", wantIs: manifest.ErrNoImage},
		{
			name:    "capability",
			archive: new(one(manifesttest.Image{BinaryPAX: map[string]string{manifesttest.CapabilityPAX: "\x01\x00\x00\x02\x00\x04\x00\x00"}})),
			arch:    "arm64", want: "frankenphp", wantIs: manifest.ErrFileCapability,
		},
		{name: "no-binary", archive: new(one(manifesttest.Image{NoBinary: true})), arch: "arm64", want: "/frankenphp"},
		{name: "records-missing", archive: new(one(manifesttest.Image{Records: without(ls, "build-manifest.json")})), arch: "arm64", want: "build-manifest.json", wantIs: fs.ErrNotExist, unreadable: true},
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
			if _, ok := errors.AsType[*manifest.UnreadableError](err); ok != tt.unreadable {
				t.Errorf("FromOCIArchive() error = %v wraps an *UnreadableError: %t, want %t", err, ok, tt.unreadable)
			}
		})
	}
}

const (
	aomOld = "dev-main+8a10385@8a1038536cde81c548341145f40d6dfddc3077d6"
	aomNew = "dev-main+81c2f0e@81c2f0e4b0c0d3a5a1e7f9b2c6d8e0a1b3c5d7f9"
	// What Go would name xcaddy's master branch a commit past v0.4.7.
	xcaddyNew = "v0.4.8-0.20261112093000-0123456789ab"
	// The schema 3 inputs after a bump of each.
	spcNew     = "0123456789abcdef0123456789abcdef01234567"
	builderNew = "sha256:00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	craftNew   = "1111111111111111111111111111111111111111111111111111111111111111"
	recipeNew  = "2222222222222222222222222222222222222222222222222222222222222222"
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
			name: "xcaddy-moved",
			edit: func(m *manifest.Manifest) { m.Xcaddy = xcaddyNew },
			want: []string{"xcaddy: v0.4.7 → " + xcaddyNew},
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
				m.Recipe = recipeNew
				m.Go = "go1.27.5"
				m.Craft = craftNew
				m.Xcaddy = xcaddyNew
				m.Builder = builderNew
				m.PHP = "8.5.13"
				m.SPC = spcNew
				m.Extensions = slices.DeleteFunc(m.Extensions, func(e string) bool { return e == "Phar" })
			},
			want: []string{
				"php: 8.5.12 → 8.5.13",
				"go: go1.27.4 → go1.27.5",
				"xcaddy: v0.4.7 → " + xcaddyNew,
				"spc: " + spcCommit + " → " + spcNew,
				"builder: " + builderDigest + " → " + builderNew,
				"craft: " + base.Craft + " → " + craftNew,
				"recipe: " + base.Recipe + " → " + recipeNew,
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

// unreadable returns the UnreadableError FromRecords gives for files.
func unreadable(t *testing.T, files map[string][]byte) *manifest.UnreadableError {
	t.Helper()
	_, err := manifest.FromRecords(rootOf(t, files))
	ue, ok := errors.AsType[*manifest.UnreadableError](err)
	if !ok {
		t.Fatalf("FromRecords() error = %v, want an *UnreadableError", err)
	}
	return ue
}

func TestChanges(t *testing.T) {
	t.Parallel()
	ls := records(t, "librespeed")
	arm := fromRecords(t, "librespeed")
	amd := clone(&arm)
	amd.Arch = "amd64"
	armMoved := clone(&arm)
	armMoved.Libraries["libaom"] = aomNew
	amdMoved := clone(&amd)
	amdMoved.PHP = "8.5.13"
	// Each schema 3 input bumped. A builder or recipe change reaches both
	// architectures; an spc or craft change is shown on one, as Changes
	// reads each architecture alone.
	moved := func(m *manifest.Manifest, edit func(*manifest.Manifest)) manifest.Manifest {
		out := clone(m)
		edit(&out)
		return out
	}
	armSPC := moved(&arm, func(m *manifest.Manifest) { m.SPC = spcNew })
	armCraft := moved(&arm, func(m *manifest.Manifest) { m.Craft = craftNew })
	setBuilder := func(m *manifest.Manifest) { m.Builder = builderNew }
	setRecipe := func(m *manifest.Manifest) { m.Recipe = recipeNew }
	// Two reads of the same records, as two builds of the same inputs give
	again := fromRecords(t, "librespeed")
	againAMD := clone(&again)
	againAMD.Arch = "amd64"

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
			name: "identical_is_empty",
			pairs: map[string]manifest.Pair{
				"amd64": {Candidate: againAMD, Published: new(amd)},
				"arm64": {Candidate: again, Published: new(arm)},
			},
			want: "",
		},
		{
			name: "spc_moved",
			pairs: map[string]manifest.Pair{
				"amd64": {Candidate: amd, Published: new(amd)},
				"arm64": {Candidate: armSPC, Published: new(arm)},
			},
			want: "arm64: spc: " + spcCommit + " → " + spcNew,
		},
		{
			name: "builder_moved",
			pairs: map[string]manifest.Pair{
				"amd64": {Candidate: moved(&amd, setBuilder), Published: new(amd)},
				"arm64": {Candidate: moved(&arm, setBuilder), Published: new(arm)},
			},
			want: "amd64: builder: " + builderDigest + " → " + builderNew + "\narm64: builder: " + builderDigest + " → " + builderNew,
		},
		{
			name: "craft_moved",
			pairs: map[string]manifest.Pair{
				"amd64": {Candidate: amd, Published: new(amd)},
				"arm64": {Candidate: armCraft, Published: new(arm)},
			},
			want: "arm64: craft: " + arm.Craft + " → " + craftNew,
		},
		{
			name: "recipe_moved",
			pairs: map[string]manifest.Pair{
				"amd64": {Candidate: moved(&amd, setRecipe), Published: new(amd)},
				"arm64": {Candidate: moved(&arm, setRecipe), Published: new(arm)},
			},
			want: "amd64: recipe: " + arm.Recipe + " → " + recipeNew + "\narm64: recipe: " + arm.Recipe + " → " + recipeNew,
		},
		{
			// What every carrier published before schema 3 holds, on
			// both architectures
			name: "schema2_published_is_a_change",
			pairs: map[string]manifest.Pair{
				"amd64": {Candidate: amd, Unreadable: unreadable(t, without(asArch(t, ls, "amd64"), schema3Records[0], schema3Records[1:]...))},
				"arm64": {Candidate: arm, Unreadable: unreadable(t, without(ls, schema3Records[0], schema3Records[1:]...))},
			},
			want: "amd64: published manifest unreadable (schema 2)\narm64: published manifest unreadable (schema 2)",
		},
		{
			// Every record there, and one this commit cannot read
			name: "unreadable_published_is_a_change",
			pairs: map[string]manifest.Pair{
				"amd64": {Candidate: amd, Published: new(amd)},
				"arm64": {Candidate: arm, Unreadable: unreadable(t, edited(t, ls, "spc.txt", spcCommit, "v3"))},
			},
			want: "arm64: published manifest unreadable (schema 3)",
		},
		{
			name: "published-and-unreadable",
			pairs: map[string]manifest.Pair{
				"amd64": {Candidate: amd, Published: new(amd)},
				"arm64": {Candidate: arm, Published: new(arm), Unreadable: unreadable(t, without(ls, "spc.txt"))},
			},
			wantErr: "arm64",
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

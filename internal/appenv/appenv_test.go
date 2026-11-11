// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package appenv_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/divbast/frankenphp/internal/appenv"
)

// good sets each input once, among a comment and a blank line.
const good = "# LibreSpeed's binary\n" +
	"\n" +
	"PHP_EXTENSIONS=ctype,gd\n" +
	"PHP_EXTENSION_LIBS=watcher,freetype\n" +
	"EXPECTED_EXTENSIONS=core,ctype,gd\n" +
	"REQUIRED_GD_FEATURES=FreeType Support,PNG Support\n"

var goodInputs = appenv.Inputs{
	PHPExtensions:      "ctype,gd",
	PHPExtensionLibs:   "watcher,freetype",
	ExpectedExtensions: "core,ctype,gd",
	RequiredGDFeatures: "FreeType Support,PNG Support",
}

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
		// want is the result when wantErr is empty.
		want appenv.Inputs
		// wantErr holds every error the file must give, and wantMsg what
		// its message must say: the file, the line and the key.
		wantErr []error
		wantMsg []string
	}{
		{name: "ok", file: good, want: goodInputs},
		{
			name: "no-trailing-newline",
			file: strings.TrimSuffix(good, "\n"),
			want: goodInputs,
		},
		{
			name: "empty-value",
			file: strings.Replace(good, "REQUIRED_GD_FEATURES=FreeType Support,PNG Support", "REQUIRED_GD_FEATURES=", 1),
			want: appenv.Inputs{
				PHPExtensions:      "ctype,gd",
				PHPExtensionLibs:   "watcher,freetype",
				ExpectedExtensions: "core,ctype,gd",
			},
		},
		{
			name: "value-verbatim",
			file: strings.Replace(good, "PHP_EXTENSION_LIBS=watcher,freetype", "PHP_EXTENSION_LIBS=a=b # c", 1),
			want: appenv.Inputs{
				PHPExtensions:      "ctype,gd",
				PHPExtensionLibs:   "a=b # c",
				ExpectedExtensions: "core,ctype,gd",
				RequiredGDFeatures: "FreeType Support,PNG Support",
			},
		},
		{
			name: "blank-line-of-spaces",
			file: "  \t\n" + good,
			want: goodInputs,
		},
		{
			name:    "pin-override",
			file:    good + "PHP_VERSION=8.5.11\n",
			wantErr: []error{appenv.ErrUnknown},
			wantMsg: []string{"test.env:7: PHP_VERSION:"},
		},
		{
			name:    "builder-override",
			file:    good + "BUILDER_IMAGE=dunglas/frankenphp:static-builder-gnu\n",
			wantErr: []error{appenv.ErrUnknown},
			wantMsg: []string{"test.env:7: BUILDER_IMAGE:"},
		},
		{
			name:    "misspelt-key",
			file:    strings.Replace(good, "REQUIRED_GD_FEATURES=", "REQUIRED_GD_FEATURE=", 1),
			wantErr: []error{appenv.ErrUnknown, appenv.ErrMissing},
			wantMsg: []string{"test.env:6: REQUIRED_GD_FEATURE:", "test.env: REQUIRED_GD_FEATURES:"},
		},
		{
			name:    "duplicate",
			file:    good + "PHP_EXTENSIONS=ctype\n",
			wantErr: []error{appenv.ErrDuplicate},
			wantMsg: []string{"test.env:7: PHP_EXTENSIONS:", "line 3"},
		},
		{
			name:    "missing",
			file:    strings.Replace(good, "PHP_EXTENSION_LIBS=watcher,freetype\n", "", 1),
			wantErr: []error{appenv.ErrMissing},
			wantMsg: []string{"test.env: PHP_EXTENSION_LIBS:"},
		},
		{
			name:    "empty-file",
			file:    "",
			wantErr: []error{appenv.ErrMissing},
			wantMsg: []string{"PHP_EXTENSIONS:", "PHP_EXTENSION_LIBS:", "EXPECTED_EXTENSIONS:", "REQUIRED_GD_FEATURES:"},
		},
		{
			name:    "quoted-value",
			file:    strings.Replace(good, "PHP_EXTENSIONS=ctype,gd", `PHP_EXTENSIONS="ctype,gd"`, 1),
			wantErr: []error{appenv.ErrQuote},
			wantMsg: []string{"test.env:3: PHP_EXTENSIONS:"},
		},
		{
			name:    "padded-value",
			file:    strings.Replace(good, "PHP_EXTENSION_LIBS=watcher,freetype", "PHP_EXTENSION_LIBS=watcher,freetype ", 1),
			wantErr: []error{appenv.ErrPadding},
			wantMsg: []string{"test.env:4: PHP_EXTENSION_LIBS:"},
		},
		{
			name:    "padded-key",
			file:    strings.Replace(good, "PHP_EXTENSION_LIBS=", " PHP_EXTENSION_LIBS =", 1),
			wantErr: []error{appenv.ErrPadding},
			wantMsg: []string{"test.env:4: PHP_EXTENSION_LIBS:"},
		},
		{
			name:    "crlf",
			file:    strings.ReplaceAll(good, "\n", "\r\n"),
			wantErr: []error{appenv.ErrPadding},
			wantMsg: []string{"test.env:3: PHP_EXTENSIONS:", "test.env:6: REQUIRED_GD_FEATURES:"},
		},
		{
			name:    "app",
			file:    good + "APP=baikal\n",
			wantErr: []error{appenv.ErrApp},
			wantMsg: []string{"test.env:7: APP:"},
		},
		{
			name:    "no-equals",
			file:    good + "PHP_EXTENSIONS\n",
			wantErr: []error{appenv.ErrMalformed},
			wantMsg: []string{"test.env:7:"},
		},
		{
			name:    "no-key",
			file:    good + "=ctype\n",
			wantErr: []error{appenv.ErrMalformed},
			wantMsg: []string{"test.env:7:"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := appenv.Parse("test.env", []byte(tt.file))
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("Parse() error = %v, want nil", err)
				}
				if got != tt.want {
					t.Errorf("Parse() = %+v, want %+v", got, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("Parse() = %+v, nil; want errors %v", got, tt.wantErr)
			}
			if got != (appenv.Inputs{}) {
				t.Errorf("Parse() = %+v alongside an error, want the zero Inputs", got)
			}
			for _, want := range tt.wantErr {
				if !errors.Is(err, want) {
					t.Errorf("Parse() error = %q, want it to be %q", err, want)
				}
			}
			for _, want := range tt.wantMsg {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Parse() error = %q, want it to say %q", err, want)
				}
			}
		})
	}
}

func TestArgs(t *testing.T) {
	t.Parallel()

	want := []string{
		"PHP_EXTENSIONS=ctype,gd",
		"PHP_EXTENSION_LIBS=watcher,freetype",
		"EXPECTED_EXTENSIONS=core,ctype,gd",
		"REQUIRED_GD_FEATURES=FreeType Support,PNG Support",
	}
	if got := goodInputs.Args(); !slices.Equal(got, want) {
		t.Errorf("Args() = %q, want %q", got, want)
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "librespeed.env"), []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.env"), []byte(good+"SPC_COMMIT=0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })

	tests := []struct {
		name    string
		app     string
		want    appenv.Inputs
		wantErr error
		wantMsg string
	}{
		{name: "ok", app: "librespeed", want: goodInputs},
		{name: "invalid", app: "bad", wantErr: appenv.ErrUnknown, wantMsg: filepath.Join(dir, "bad.env") + ":7: SPC_COMMIT:"},
		{name: "absent", app: "nosuch", wantErr: fs.ErrNotExist, wantMsg: "nosuch.env"},
		{name: "outside", app: "../librespeed", wantErr: appenv.ErrAppName},
		{name: "path", app: "librespeed/x", wantErr: appenv.ErrAppName},
		{name: "empty", app: "", wantErr: appenv.ErrAppName},
		{name: "upper-case", app: "LibreSpeed", wantErr: appenv.ErrAppName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := appenv.Load(root, tt.app)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Load(%q) error = %v, want nil", tt.app, err)
				}
				if got != tt.want {
					t.Errorf("Load(%q) = %+v, want %+v", tt.app, got, tt.want)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Load(%q) error = %v, want %q", tt.app, err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("Load(%q) error = %q, want it to say %q", tt.app, err, tt.wantMsg)
			}
		})
	}
}

// TestRepositoryApps parses every apps/*.env this repository builds.
func TestRepositoryApps(t *testing.T) {
	t.Parallel()

	root, err := os.OpenRoot(filepath.Join("..", "..", "apps"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	files, err := fs.Glob(root.FS(), "*.env")
	if err != nil {
		t.Fatal(err)
	}
	apps := make([]string, 0, len(files))
	for _, file := range files {
		apps = append(apps, strings.TrimSuffix(file, ".env"))
	}
	if want := []string{"baikal", "librespeed"}; !slices.Equal(apps, want) {
		t.Fatalf("apps/*.env = %q, want %q", apps, want)
	}

	for _, app := range apps {
		t.Run(app, func(t *testing.T) {
			t.Parallel()

			in, err := appenv.Load(root, app)
			if err != nil {
				t.Fatalf("Load(%q) error = %v, want nil", app, err)
			}
			if in.PHPExtensions == "" || in.PHPExtensionLibs == "" || in.ExpectedExtensions == "" {
				t.Errorf("Load(%q) = %+v, want PHP_EXTENSIONS, PHP_EXTENSION_LIBS and EXPECTED_EXTENSIONS set", app, in)
			}
		})
	}
}

// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	files := map[string]string{
		"librespeed.env": "# comment\n" +
			"PHP_EXTENSIONS=ctype,gd\n" +
			"PHP_EXTENSION_LIBS=watcher,freetype\n" +
			"EXPECTED_EXTENSIONS=core,ctype,gd\n" +
			"REQUIRED_GD_FEATURES=FreeType Support,PNG Support",
		"pinned.env": "PHP_EXTENSIONS=ctype\n" +
			"PHP_EXTENSION_LIBS=watcher\n" +
			"EXPECTED_EXTENSIONS=core,ctype\n" +
			"REQUIRED_GD_FEATURES=\n" +
			"PHP_VERSION=8.5.11\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name:     "ok",
			args:     []string{"-dir", dir, "librespeed"},
			wantCode: 0,
			wantStdout: "PHP_EXTENSIONS=ctype,gd\n" +
				"PHP_EXTENSION_LIBS=watcher,freetype\n" +
				"EXPECTED_EXTENSIONS=core,ctype,gd\n" +
				"REQUIRED_GD_FEATURES=FreeType Support,PNG Support\n",
		},
		{
			name:       "refused",
			args:       []string{"-dir", dir, "pinned"},
			wantCode:   1,
			wantStderr: filepath.Join(dir, "pinned.env") + ":5: PHP_VERSION:",
		},
		{
			name:       "absent",
			args:       []string{"-dir", dir, "nosuch"},
			wantCode:   1,
			wantStderr: "nosuch.env",
		},
		{
			name:       "bad-name",
			args:       []string{"-dir", dir, "../librespeed"},
			wantCode:   1,
			wantStderr: "../librespeed",
		},
		{
			name:       "no-app",
			args:       []string{"-dir", dir},
			wantCode:   2,
			wantStderr: "usage",
		},
		{
			name:       "two-apps",
			args:       []string{"-dir", dir, "librespeed", "baikal"},
			wantCode:   2,
			wantStderr: "usage",
		},
		{
			name:       "bad-flag",
			args:       []string{"-nosuch"},
			wantCode:   2,
			wantStderr: "nosuch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr strings.Builder
			if code := run(tt.args, &stdout, &stderr); code != tt.wantCode {
				t.Errorf("run(%q) = %d, want %d; stderr:\n%s", tt.args, code, tt.wantCode, stderr.String())
			}
			if got := stdout.String(); got != tt.wantStdout {
				t.Errorf("run(%q) stdout = %q, want %q", tt.args, got, tt.wantStdout)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("run(%q) stderr = %q, want it to say %q", tt.args, stderr.String(), tt.wantStderr)
			}
		})
	}
}

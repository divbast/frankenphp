// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package build

import (
	"strings"
	"testing"
)

// TestContainerfilePkgRoot holds the Containerfile to the go-xcaddy package
// spc actually runs. spc unpacks it under PKG_ROOT_PATH, which the builder
// image sets, and runs `xcaddy build` from there; a path written out in full
// would keep pointing at the old place, and keep checking an xcaddy nothing
// ran, if the builder ever moved it.
func TestContainerfilePkgRoot(t *testing.T) {
	t.Parallel()

	containerfile := read(t, "../Containerfile")
	for i, line := range strings.Split(containerfile, "\n") {
		if strings.Contains(line, "/opt/spc-pkgroot") {
			t.Errorf("Containerfile:%d names spc's package root as a path, not as ${PKG_ROOT_PATH}: %s", i+1, strings.TrimSpace(line))
		}
	}
	for _, input := range []string{
		`GO_BIN="${PKG_ROOT_PATH}/go-xcaddy/bin/go"`,
		`XCADDY_BIN="${PKG_ROOT_PATH}/go-xcaddy/bin/xcaddy"`,
	} {
		// pinned-versions.sh and records.sh each read both.
		if n := strings.Count(containerfile, input); n != 2 {
			t.Errorf("Containerfile passes %s %d times, want 2: once to pinned-versions.sh, once to records.sh", input, n)
		}
	}
}

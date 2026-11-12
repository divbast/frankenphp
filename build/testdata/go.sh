#!/usr/bin/env bash
# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Stands in for spc's go in checks_test.go. `go version -m FILE` prints the
# canned build info of xcaddy from the directory STUB_ANSWERS names,
# captured from a build's own xcaddy, and like go it wants FILE to exist.
set -euo pipefail

answers="${STUB_ANSWERS:?the directory of canned answers}"
if [[ $# -eq 3 && "${1}" == version && "${2}" == -m && -f "${3}" ]]; then
	cat "${answers}/xcaddy"
else
	echo "stub go: no canned answer for: $*" >&2
	exit 2
fi

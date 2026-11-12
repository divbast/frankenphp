#!/usr/bin/env bash
# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Stands in for docker in justfile_test.go. It writes to STUB_ENGINE_LOG
# whether GITHUB_TOKEN reached it, never its value, then each argument it
# was given, one per line.
set -euo pipefail

log="${STUB_ENGINE_LOG:?the file to write the call to}"
{
	if [[ -n "${GITHUB_TOKEN-}" ]]; then
		echo "GITHUB_TOKEN is set"
	else
		echo "GITHUB_TOKEN is empty"
	fi
	printf '%s\n' "$@"
} >"${log}"

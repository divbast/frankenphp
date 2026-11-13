#!/usr/bin/env bash
# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Stands in for docker in publish_test.go. It records each call in
# STUB_LOG, then fails when STUB_DOCKER_FAIL is set, and otherwise prints
# the file STUB_DOCKER_BYTES names and a newline, as buildx's
# `imagetools create --dry-run` prints the index it would push.
set -euo pipefail

printf 'docker %s\n' "$*" >>"${STUB_LOG:?the file the stubs record their calls in}"
if [[ -n "${STUB_DOCKER_FAIL-}" ]]; then
	echo "stub docker: dry run failed" >&2
	exit 1
fi
cat "${STUB_DOCKER_BYTES:?the index to print}"
printf '\n'

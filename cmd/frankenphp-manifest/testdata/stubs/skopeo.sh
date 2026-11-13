#!/usr/bin/env bash
# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Stands in for skopeo in publish_test.go. It records each call in
# STUB_LOG, then fails when STUB_SKOPEO_FAIL is set, as for an image no
# anonymous client can read, and otherwise prints the file
# STUB_SKOPEO_BYTES names, as `skopeo inspect --raw` prints a manifest.
set -euo pipefail

printf 'skopeo %s\n' "$*" >>"${STUB_LOG:?the file the stubs record their calls in}"
if [[ -n "${STUB_SKOPEO_FAIL-}" ]]; then
	echo "stub skopeo: unauthorized" >&2
	exit 1
fi
cat "${STUB_SKOPEO_BYTES:?the manifest to print}"

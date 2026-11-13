#!/usr/bin/env bash
# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Stands in for gh in publish_test.go. It records each call in STUB_LOG,
# then fails the first STUB_GH_FAILS calls, or every call when that is
# "all", and passes the rest.
set -euo pipefail

log="${STUB_LOG:?the file the stubs record their calls in}"
printf 'gh %s\n' "$*" >>"${log}"
calls=$(grep -c '^gh ' "${log}")
fails="${STUB_GH_FAILS:-0}"
if [[ "${fails}" == all ]] || ((calls <= fails)); then
	echo "stub gh: refusing call ${calls}" >&2
	exit 1
fi

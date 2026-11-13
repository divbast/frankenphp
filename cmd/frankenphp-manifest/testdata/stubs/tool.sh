#!/usr/bin/env bash
# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Stands in, in publish_test.go, for the decide step's other commands, by
# the name it runs as: git, go, mise, and divbast/ci's
# divbast-published-revision and divbast-publish-decision. It records
# each call in STUB_LOG. Then divbast-published-revision prints
# STUB_PUBLISHED, as the commit the published image was built from, empty
# when nothing is; go prints
# STUB_CHANGE, as frankenphp-manifest -changes prints what moved; and
# divbast-publish-decision writes its arguments to STUB_DECISION_ARGS, each
# ended by a NUL, since one may hold newlines or nothing at all.
set -euo pipefail

name=$(basename "$0")
printf '%s %s\n' "${name}" "$*" >>"${STUB_LOG:?the file the stubs record their calls in}"
case "${name}" in
git) ;;
mise)
	# mise exec -- COMMAND...: runs the command, as the real one does
	shift 2
	exec "$@"
	;;
divbast-published-revision) printf '%s' "${STUB_PUBLISHED-}" ;;
go) printf '%s\n' "${STUB_CHANGE-}" ;;
divbast-publish-decision)
	printf '%s\0' "$@" >"${STUB_DECISION_ARGS:?the file to write the decision arguments to}"
	;;
*)
	echo "stub: no stand-in for ${name}" >&2
	exit 2
	;;
esac

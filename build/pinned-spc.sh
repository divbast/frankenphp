#!/usr/bin/env bash
# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Fails the build unless static-php-cli at the commit SPC_COMMIT pins, the
# build stage's ARG, built the binary.
#
# build-static.sh downloads a nightly spc into dist/spc unless one is
# already executable there, and clones static-php-cli's v3 branch into
# dist/static-php-cli if that download fails. Either would build the binary
# with an spc no pin names. The wrapper the Containerfile puts at dist/spc
# records each subcommand it runs, so a craft run on that record is proof
# the pinned spc did the build, not merely that a file was in place.
set -euo pipefail

SRC="${SPC_SRC:-/opt/static-php-cli}"
WRAPPER="${SPC_WRAPPER:-dist/spc}"
CALLS="${SPC_CALLS:-dist/spc.calls}"
CLONE="${SPC_CLONE:-dist/static-php-cli}"
FAILED=0

fail() {
	echo "ERROR: ${1}" >&2
	FAILED=1
}

if [[ -z "${SPC_COMMIT-}" ]]; then
	fail "SPC_COMMIT is empty; the Containerfile must pin static-php-cli's commit."
	exit 1
fi

if ! grep -qxF "exec php ${SRC}/bin/spc \"\$@\"" "${WRAPPER}" 2>/dev/null; then
	fail "${WRAPPER} is not the wrapper around ${SRC}: build-static.sh fetched its own spc, not the one SPC_COMMIT pins."
fi
if ! grep -qxF craft "${CALLS}" 2>/dev/null; then
	fail "the wrapper never ran spc craft: something other than the spc SPC_COMMIT pins built the binary."
fi
if [[ -e "${CLONE}" ]]; then
	fail "build-static.sh cloned static-php-cli's v3 branch into ${CLONE}, which SPC_COMMIT does not pin."
fi
head=$(git -C "${SRC}" rev-parse --verify --quiet HEAD 2>/dev/null) || true
if [[ "${head}" != "${SPC_COMMIT}" ]]; then
	fail "${SRC} is at ${head:-no checkout}, but SPC_COMMIT pins ${SPC_COMMIT}."
elif ! git -C "${SRC}" diff --quiet HEAD --; then
	fail "${SRC} has changes to tracked files on top of the commit SPC_COMMIT pins."
fi

if ((FAILED)); then
	exit 1
fi
echo "the binary was built by static-php-cli ${SPC_COMMIT}"

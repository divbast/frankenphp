#!/usr/bin/env bash
# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Writes the records of how the binary was built into RECORDS, which the
# carrier ships beside it:
#
#   app.txt              APP, the build stage's ARG: whose inputs built it
#   version.txt          `frankenphp version`: FrankenPHP, PHP and Caddy
#   build-info.txt       `frankenphp build-info`: Go and every Go module
#   xcaddy.txt           `go version -m` of spc's xcaddy, which wrote the
#                        binary's main module and ran its build
#   extensions.txt       every module it loads, one per line, sorted
#   spc-cache.json       spc's download cache: each source, its version and hash
#   build-manifest.json  spc's build manifest: every library it built
#
# spc resolves several library sources to their newest release at build
# time, so its two files are the only record of which C libraries the
# binary holds. It installs xcaddy from its master branch whenever it
# fetches a newer Go than the builder's, and otherwise keeps the builder's
# own, so xcaddy.txt is the only record of which commit built it. A record
# set is whole or absent: nothing is written unless everything can be.
set -euo pipefail
export LC_ALL=C

BIN="${FRANKENPHP_BIN:-dist/frankenphp}"
CACHE="${SPC_CACHE:-dist/downloads/.cache.json}"
MANIFEST="${BUILD_MANIFEST:-dist/buildroot/build-manifest.json}"
# spc's go-xcaddy package, in the builder's PKG_ROOT_PATH
GO="${GO_BIN:-/opt/spc-pkgroot/go-xcaddy/bin/go}"
XCADDY="${XCADDY_BIN:-/opt/spc-pkgroot/go-xcaddy/bin/xcaddy}"
OUT="${RECORDS:-/records}"
FAILED=0

fail() {
	echo "ERROR: ${1}" >&2
	FAILED=1
}

if [[ -z "${APP-}" ]]; then
	fail "APP is empty; the build must name the app whose inputs it passes."
fi
if [[ ! -x "${BIN}" ]]; then
	fail "FRANKENPHP_BIN names no executable binary: ${BIN}"
fi
if [[ ! -s "${CACHE}" ]]; then
	fail "spc left no download cache at ${CACHE}."
fi
if [[ ! -s "${MANIFEST}" ]]; then
	fail "spc left no build manifest at ${MANIFEST}."
fi
if [[ ! -x "${GO}" ]]; then
	fail "GO_BIN names no executable go: ${GO}"
fi
if [[ ! -f "${XCADDY}" ]]; then
	fail "XCADDY_BIN names no xcaddy: ${XCADDY}"
fi
if [[ -e "${OUT}" ]]; then
	fail "${OUT} already exists; records are written once, by one build."
fi
if ((FAILED)); then
	exit 1
fi

# record FILE COMMAND...: FILE holds what COMMAND prints.
record() {
	local file="${1}"
	shift
	if ! "$@" >"${staging}/${file}"; then
		echo "ERROR: ${*} failed, so ${file} has no record." >&2
		exit 1
	fi
}

extensions() {
	"${BIN}" php-cli -r 'echo implode("\n", get_loaded_extensions()), "\n";' | sort
}

# Written beside RECORDS and renamed into place once complete.
mkdir -p "$(dirname "${OUT}")"
staging=$(mktemp -d "${OUT}.XXXXXX")
trap 'rm -rf "${staging}"' EXIT
chmod 0755 "${staging}"
record app.txt printf '%s\n' "${APP}"
record version.txt "${BIN}" version
record build-info.txt "${BIN}" build-info
record xcaddy.txt "${GO}" version -m "${XCADDY}"
record extensions.txt extensions
record spc-cache.json cat "${CACHE}"
record build-manifest.json cat "${MANIFEST}"
mv "${staging}" "${OUT}"
echo "records for ${APP} written to ${OUT}"

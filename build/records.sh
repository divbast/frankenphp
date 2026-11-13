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
#   spc.txt              SPC_COMMIT: the static-php-cli that built it
#   builder.txt          BUILDER_IMAGE: the builder image, by digest
#   craft.yml            the craft.yml build-static.sh rendered for spc
#   recipe.txt           RECIPE_SHA256: the sha256 of the Containerfile and
#                        apps/<app>.env, as the build hashed them
#
# spc resolves several library sources to their newest release at build
# time, so its two files are the only record of which C libraries the
# binary holds. xcaddy.txt records the xcaddy that built it, which
# pinned-versions.sh has already held to XCADDY_VERSION. The last four are
# what decides how the binary is built without the binary reporting it:
# spc's build recipes, the builder's compilers and headers, the flags spc
# was given, and every pin and app input. A record set is whole or absent:
# nothing is written unless everything can be.
set -euo pipefail
export LC_ALL=C

BIN="${FRANKENPHP_BIN:-dist/frankenphp}"
CACHE="${SPC_CACHE:-dist/downloads/.cache.json}"
MANIFEST="${BUILD_MANIFEST:-dist/buildroot/build-manifest.json}"
CRAFT_YML="${CRAFT:-dist/craft.yml}"
# spc's go-xcaddy package, in the builder's PKG_ROOT_PATH
GO="${GO_BIN:-${PKG_ROOT_PATH:-/opt/spc-pkgroot}/go-xcaddy/bin/go}"
XCADDY="${XCADDY_BIN:-${PKG_ROOT_PATH:-/opt/spc-pkgroot}/go-xcaddy/bin/xcaddy}"
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
# The go-xcaddy step writes a cache before the craft runs, holding
# go-xcaddy alone, so a cache being there proves nothing: only one that
# lists php-src, which every craft fetches and install-pkg never does, is
# the craft's. jq says above the ERROR line when it cannot read the file.
if [[ ! -s "${CACHE}" ]]; then
	fail "spc left no download cache at ${CACHE}."
elif ! jq -e 'type == "object" and has("php-src")' "${CACHE}" >/dev/null; then
	fail "${CACHE} has no php-src, which every spc craft fetches: the craft did not write this download cache."
fi
if [[ ! -s "${MANIFEST}" ]]; then
	fail "spc left no build manifest at ${MANIFEST}."
fi
if [[ ! -s "${CRAFT_YML}" ]]; then
	fail "build-static.sh left no rendered craft.yml at ${CRAFT_YML}."
fi
# The build's own inputs, which the records stage declares as ARGs.
if [[ ! "${SPC_COMMIT-}" =~ ^[0-9a-f]{40}$ ]]; then
	fail "SPC_COMMIT is \"${SPC_COMMIT-}\", not the full commit static-php-cli was built from."
fi
if [[ ! "${BUILDER_IMAGE-}" =~ @sha256:[0-9a-f]{64}$ ]]; then
	fail "BUILDER_IMAGE is \"${BUILDER_IMAGE-}\", which names no digest."
fi
if [[ ! "${RECIPE_SHA256-}" =~ ^[0-9a-f]{64}$ ]]; then
	fail "RECIPE_SHA256 is \"${RECIPE_SHA256-}\", not a sha256; build.yml and just build-app pass sha256sum's hash of the Containerfile and apps/<app>.env, without its file name."
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
record spc.txt printf '%s\n' "${SPC_COMMIT}"
record builder.txt printf '%s\n' "${BUILDER_IMAGE}"
record craft.yml cat "${CRAFT_YML}"
record recipe.txt printf '%s\n' "${RECIPE_SHA256}"
mv "${staging}" "${OUT}"
echo "records for ${APP} written to ${OUT}"

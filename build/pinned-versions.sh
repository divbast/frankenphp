#!/usr/bin/env bash
# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Fails the build unless the binary, and the xcaddy that built it, report
# exactly the versions the Containerfile pins. The pins are the build
# stage's ARGs, which RUN sees as environment variables. FRANKENPHP_BIN
# names the binary, and GO_BIN and XCADDY_BIN spc's go and xcaddy.
#
# A pin only means something if the binary agrees with it. Minimum version
# selection lifts a module above its pin whenever something else in the
# graph requires more, and the build then ships a version no ARG names.
set -euo pipefail

BIN="${FRANKENPHP_BIN:-dist/frankenphp}"
# spc's go-xcaddy package, in the builder's PKG_ROOT_PATH
GO="${GO_BIN:-/opt/spc-pkgroot/go-xcaddy/bin/go}"
XCADDY="${XCADDY_BIN:-/opt/spc-pkgroot/go-xcaddy/bin/xcaddy}"
PINS=(PHP_VERSION CADDY_VERSION CBROTLI_VERSION MERCURE_VERSION VULCAIN_VERSION XCADDY_VERSION)
FAILED=0

fail() {
	echo "ERROR: ${1}" >&2
	FAILED=1
}

# An empty pin would let this check pass whatever the binary holds.
for pin in "${PINS[@]}"; do
	if [[ -z "${!pin-}" ]]; then
		fail "${pin} is empty; the Containerfile must pin it."
	fi
done
if [[ ! -x "${BIN}" ]]; then
	fail "FRANKENPHP_BIN names no executable binary: ${BIN}"
fi
if [[ ! -x "${GO}" ]]; then
	fail "GO_BIN names no executable go: ${GO}"
fi
if [[ ! -f "${XCADDY}" ]]; then
	fail "XCADDY_BIN names no xcaddy: ${XCADDY}"
fi
if ((FAILED)); then
	exit 1
fi

version=$("${BIN}" version)
info=$("${BIN}" build-info)
if ! xcaddy=$("${GO}" version -m "${XCADDY}"); then
	fail "go version -m cannot read the build info of XCADDY_BIN, ${XCADDY}."
	exit 1
fi

# check ARG WHAT FOUND [WHO]: FOUND is the version of WHAT that WHO, the
# binary unless named, reports, and ARG the Containerfile's pin for it.
check() {
	local pin="${1}" what="${2}" found="${3}" who="${4:-the binary}"
	if [[ -z "${found}" ]]; then
		fail "${who} reports no ${what}, but ${pin} pins ${!pin}."
	elif [[ "${found}" != "${!pin}" ]]; then
		fail "${who} has ${what} ${found}, but ${pin} pins ${!pin}."
	fi
}

# build-info prints one "dep<TAB>module<TAB>version<TAB>sum" line per
# module, and a "=><TAB>path<TAB>version<TAB>sum" line after any the build
# replaced. A replaced module's code is the replacement's, so its version
# reads "version => path version", which matches no pin.
dep() {
	awk -F'\t' -v module="${1}" '
		$1 == "dep" { hit = ($2 == module); if (hit) found = $3; next }
		$1 == "=>" && hit { found = found " => " $2 " " $3; hit = 0; next }
		{ hit = 0 }
		END { print found }' <<<"${info}"
}

# version prints "FrankenPHP vX PHP X.Y.Z Caddy vX h1:...".
php=""
if [[ "${version}" =~ (^|[[:space:]])PHP[[:space:]]([0-9]+\.[0-9]+\.[0-9]+)([[:space:]]|$) ]]; then
	php="${BASH_REMATCH[2]}"
fi
check PHP_VERSION PHP "${php}"
for module in CADDY_VERSION=github.com/caddyserver/caddy/v2 \
	CBROTLI_VERSION=github.com/dunglas/caddy-cbrotli \
	MERCURE_VERSION=github.com/dunglas/mercure/caddy \
	VULCAIN_VERSION=github.com/dunglas/vulcain/caddy; do
	found=$(dep "${module#*=}")
	check "${module%%=*}" "${module#*=}" "${found}"
done
# go version -m prints xcaddy's own module as
# "<TAB>mod<TAB>github.com/caddyserver/xcaddy<TAB>version<TAB>sum".
found=$(awk -F'\t' '$2 == "mod" && $3 == "github.com/caddyserver/xcaddy" { print $4 }' <<<"${xcaddy}")
if [[ "${found}" != "${XCADDY_VERSION-}" ]]; then
	check XCADDY_VERSION github.com/caddyserver/xcaddy "${found}" "spc's xcaddy, which built the binary,"
	echo "spc installs xcaddy from its master branch whenever it unpacks its" >&2
	echo "go-xcaddy package again: the Containerfile's go-xcaddy step must leave" >&2
	echo "it nothing to unpack." >&2
fi

if ((FAILED)); then
	echo "Raise each ARG named above to the version the binary reports, or" >&2
	echo "find what in the module graph now requires more, or replaces it." >&2
	exit 1
fi
summary=""
for pin in "${PINS[@]}"; do
	summary+=" ${pin}=${!pin}"
done
echo "the binary and xcaddy match every pin:${summary}"

#!/usr/bin/env bash
# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Fails the build unless the binary reports exactly the versions the
# Containerfile pins. The pins are the build stage's ARGs, which RUN sees as
# environment variables, and FRANKENPHP_BIN names the binary.
#
# A pin only means something if the binary agrees with it. Minimum version
# selection lifts a module above its pin whenever something else in the
# graph requires more, and the build then ships a version no ARG names.
set -euo pipefail

BIN="${FRANKENPHP_BIN:-dist/frankenphp}"
PINS=(PHP_VERSION CADDY_VERSION CBROTLI_VERSION MERCURE_VERSION VULCAIN_VERSION)
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
if ((FAILED)); then
	exit 1
fi

version=$("${BIN}" version)
info=$("${BIN}" build-info)

# check ARG WHAT FOUND: FOUND is the version of WHAT the binary reports,
# and ARG the Containerfile's pin for it.
check() {
	local pin="${1}" what="${2}" found="${3}"
	if [[ -z "${found}" ]]; then
		fail "the binary reports no ${what}, but ${pin} pins ${!pin}."
	elif [[ "${found}" != "${!pin}" ]]; then
		fail "the binary has ${what} ${found}, but ${pin} pins ${!pin}."
	fi
}

# build-info prints one "dep<TAB>module<TAB>version<TAB>sum" line per module.
dep() {
	awk -F'\t' -v module="${1}" '$1 == "dep" && $2 == module { print $3 }' <<<"${info}"
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

if ((FAILED)); then
	echo "Raise each ARG named above to the version the binary reports, or" >&2
	echo "find what in the module graph now requires more." >&2
	exit 1
fi
summary=""
for pin in "${PINS[@]}"; do
	summary+=" ${pin}=${!pin}"
done
echo "the binary matches every pin:${summary}"

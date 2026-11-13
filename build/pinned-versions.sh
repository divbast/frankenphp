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
GO="${GO_BIN:-${PKG_ROOT_PATH:-/opt/spc-pkgroot}/go-xcaddy/bin/go}"
XCADDY="${XCADDY_BIN:-${PKG_ROOT_PATH:-/opt/spc-pkgroot}/go-xcaddy/bin/xcaddy}"
PINS=(PHP_VERSION CADDY_VERSION CBROTLI_VERSION MERCURE_VERSION VULCAIN_VERSION XCADDY_VERSION)
FAILED=0
# Each kind of failure has its own fix, so each sets its own flag and the
# advice at the end follows the kinds that happened.
PHP_WRONG=0 LIFTED=0 MISSING=0 REPLACED=0 XCADDY_WRONG=0

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

# check ARG MODULE FOUND: FOUND is the version of the Go module MODULE the
# binary reports, as dep prints it, and ARG the Containerfile's pin for it.
check() {
	local pin="${1}" module="${2}" found="${3}"
	if [[ -z "${found}" ]]; then
		fail "the binary reports no ${module}, but ${pin} pins ${!pin}."
		MISSING=1
	elif [[ "${found}" == *" => "* ]]; then
		fail "the binary has ${module} replaced (${found}), but ${pin} pins ${!pin}."
		REPLACED=1
	elif [[ "${found}" != "${!pin}" ]]; then
		fail "the binary has ${module} ${found}, but ${pin} pins ${!pin}."
		LIFTED=1
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
if [[ -z "${php}" ]]; then
	fail "the binary reports no PHP version, but PHP_VERSION pins ${PHP_VERSION-}."
	PHP_WRONG=1
elif [[ "${php}" != "${PHP_VERSION-}" ]]; then
	fail "the binary has PHP ${php}, but PHP_VERSION pins ${PHP_VERSION-}."
	PHP_WRONG=1
fi
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
if [[ -z "${found}" ]]; then
	fail "spc's xcaddy, which built the binary, names no github.com/caddyserver/xcaddy module, but XCADDY_VERSION pins ${XCADDY_VERSION-}."
	XCADDY_WRONG=1
elif [[ "${found}" != "${XCADDY_VERSION-}" ]]; then
	fail "spc's xcaddy, which built the binary, is github.com/caddyserver/xcaddy ${found}, but XCADDY_VERSION pins ${XCADDY_VERSION-}."
	XCADDY_WRONG=1
fi

if ((FAILED)); then
	if ((PHP_WRONG)); then
		echo "build-static.sh passes PHP_VERSION to spc as craft.yml's php-version, so a binary with another PHP, or none it reports, means that no longer holds: find what changed it." >&2
	fi
	if ((LIFTED)); then
		echo "Go's minimum version selection lifts a module above its pin when something else in the graph requires more: raise its ARG to the version the binary reports, or find what requires it." >&2
	fi
	if ((MISSING)); then
		echo "A pin the binary reports nothing for checks nothing: find out why the build no longer includes that module, rather than dropping the pin." >&2
	fi
	if ((REPLACED)); then
		echo "A replaced module's code is the replacement's, not the version its pin names, whatever version the replacement claims: remove the replace from the build, or relax this check deliberately." >&2
	fi
	if ((XCADDY_WRONG)); then
		echo "The build installs XCADDY_VERSION itself, and spc builds xcaddy from its master branch whenever it unpacks its go-xcaddy package again: fix the Containerfile's go-xcaddy step so the craft finds nothing to unpack, and don't raise XCADDY_VERSION to what spc built." >&2
	fi
	exit 1
fi
summary=""
for pin in "${PINS[@]}"; do
	summary+=" ${pin}=${!pin}"
done
echo "the binary and xcaddy match every pin:${summary}"

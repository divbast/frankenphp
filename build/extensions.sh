#!/usr/bin/env bash
# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Fails the build unless the binary loads exactly the modules
# EXPECTED_EXTENSIONS names, and gd supports every feature
# REQUIRED_GD_FEATURES names. Both are the build stage's ARGs, from
# apps/<app>.env, which RUN sees as environment variables, and
# FRANKENPHP_BIN names the binary.
#
# EXPECTED_EXTENSIONS names every module, even those every PHP 8.5 build and
# FrankenPHP carry. Each is code the image ships, so none may arrive
# unnamed, as something's dependency or one of spc's suggestions.
set -euo pipefail
export LC_ALL=C

BIN="${FRANKENPHP_BIN:-dist/frankenphp}"
FAILED=0

fail() {
	echo "ERROR: ${1}" >&2
	FAILED=1
}

if [[ -z "${EXPECTED_EXTENSIONS-}" ]]; then
	fail "EXPECTED_EXTENSIONS is empty; apps/<app>.env must name every module the binary loads."
fi
if [[ ! -x "${BIN}" ]]; then
	fail "FRANKENPHP_BIN names no executable binary: ${BIN}"
fi
if ((FAILED)); then
	exit 1
fi

# One module per line, in lower case and sorted. get_loaded_extensions()
# capitalises a few, and names OPcache after its zend_extension entry.
normalise() {
	tr '[:upper:]' '[:lower:]' |
		sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e 's/^zend opcache$/opcache/' -e '/^$/d' |
		sort -u
}

# php-cli -m answers only from PHP 8.6 on, so ask PHP itself.
loaded=$("${BIN}" php-cli -r 'echo implode("\n", get_loaded_extensions()), "\n";')
found=$(normalise <<<"${loaded}")
wanted=$(tr ',' '\n' <<<"${EXPECTED_EXTENSIONS}" | normalise)
extra=$(comm -23 <(printf '%s\n' "${found}") <(printf '%s\n' "${wanted}"))
missing=$(comm -13 <(printf '%s\n' "${found}") <(printf '%s\n' "${wanted}"))
while IFS= read -r module; do
	if [[ -n "${module}" ]]; then
		fail "the binary loads ${module}, which EXPECTED_EXTENSIONS does not name."
	fi
done <<<"${extra}"
while IFS= read -r module; do
	if [[ -n "${module}" ]]; then
		fail "EXPECTED_EXTENSIONS names ${module}, which the binary does not load."
	fi
done <<<"${missing}"

# gd_info()'s own report: one line per feature it supports, and none at all
# when the binary has no gd.
if [[ -n "${REQUIRED_GD_FEATURES-}" ]]; then
	read -r -d '' report <<'PHP' || true
foreach (function_exists('gd_info') ? gd_info() : [] as $feature => $on) {
    if ($on === true) {
        echo $feature, "\n";
    }
}
PHP
	features=$("${BIN}" php-cli -r "${report}")
	IFS=',' read -r -a required <<<"${REQUIRED_GD_FEATURES}"
	for feature in "${required[@]}"; do
		if [[ -n "${feature}" ]] && ! grep -qxF "${feature}" <<<"${features}"; then
			fail "gd_info() reports no ${feature}, which REQUIRED_GD_FEATURES asks for."
		fi
	done
fi

if ((FAILED)); then
	echo "Make EXPECTED_EXTENSIONS name exactly what the binary should load, and" >&2
	echo "PHP_EXTENSIONS and PHP_EXTENSION_LIBS build that and what" >&2
	echo "REQUIRED_GD_FEATURES asks of gd." >&2
	exit 1
fi
modules=$(paste -sd ' ' - <<<"${found}")
echo "the binary loads exactly EXPECTED_EXTENSIONS: ${modules}"
if [[ -n "${REQUIRED_GD_FEATURES-}" ]]; then
	echo "gd supports ${REQUIRED_GD_FEATURES}"
fi

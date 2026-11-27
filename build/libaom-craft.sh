#!/usr/bin/env bash
# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Adds libaom, at the AOM release LIBAOM_TAG pins, the build stage's ARG,
# to the custom-git entries in download-options of the craft.yml CRAFT
# names, which spc clones each at its ref. It runs before build-static.sh,
# which copies every line it does not rewrite.
#
# The builder's craft.yml comes in two shapes. With no custom-git key, as up
# to 1.13.6, this adds one under download-options, a one-line list holding
# libaom. With a custom-git list there, as 1.14.0 has for extensions that do
# not build with PHP 8.6 yet and marks as temporary, this adds libaom as the
# list's first entry: spc's YAML parser refuses a second custom-git key. Any
# other shape fails, as does a craft.yml that names libaom itself, and
# craft.yml is then left as it was. spc ignores an entry, without a word,
# when no package it downloads has that name: pinned-libaom.sh checks the
# clone after the build.
set -euo pipefail

CRAFT_YML="${CRAFT:-craft.yml}"

fail() {
	echo "ERROR: ${1}" >&2
	exit 1
}

if [[ -z "${LIBAOM_TAG-}" ]]; then
	fail "LIBAOM_TAG is empty; the Containerfile must pin libaom to an AOM release tag."
fi
if [[ ! -s "${CRAFT_YML}" ]]; then
	fail "CRAFT names no craft.yml: ${CRAFT_YML}"
fi

entry="libaom:${LIBAOM_TAG}:https://aomedia.googlesource.com/aom"
# A custom-git key at any depth, whatever its value.
key='^[[:space:]]*custom-git:'
if grep -qE "${key}" "${CRAFT_YML}"; then
	after="  custom-git:"
	line="    - \"${entry}\""
	missing="${CRAFT_YML} has custom-git in a shape this script does not know: it adds libaom only to a list directly under download-options, written as \"  custom-git:\" with one \"    - \" entry per line."
else
	after="download-options:"
	line="  custom-git: [\"${entry}\"]"
	missing="${CRAFT_YML} has no \"download-options:\" line, alone on its line, to add custom-git under."
fi

# Copy every line, adding the new one once: below the first line that is
# exactly ${after} within download-options, and in a list only above an
# entry indented as the new one is.
patched=$(mktemp)
trap 'rm -f "${patched}"' EXIT
section=""
pending=0
added=0
while IFS= read -r text || [[ -n "${text}" ]]; do
	if ((pending)); then
		pending=0
		if [[ "${text}" == "    - "* ]]; then
			printf '%s\n' "${line}"
			added=1
		fi
	fi
	printf '%s\n' "${text}"
	if [[ "${text}" =~ ^[^[:space:]#] ]]; then
		section="${text}"
	fi
	if ((! added)) && [[ "${text}" == "${after}" && "${section}" == "download-options:" ]]; then
		if [[ "${after}" == "download-options:" ]]; then
			printf '%s\n' "${line}"
			added=1
		else
			pending=1
		fi
	fi
done <"${CRAFT_YML}" >"${patched}"

# Whether this run added the line, not whether the output holds it: the
# entry already there, in a shape this script does not know, is no pass.
if ((! added)); then
	fail "${missing}"
fi
keys=$(grep -cE "${key}" "${patched}" || true)
if ((keys != 1)); then
	fail "${CRAFT_YML} has ${keys} custom-git keys, want one: spc's YAML parser refuses a duplicate key, so libaom's entry must join the one list."
fi
named=$(grep -c 'libaom:' "${patched}" || true)
if ((named != 1)); then
	fail "${CRAFT_YML} names libaom ${named} times with the entry this adds, want once: the builder's craft.yml names libaom itself, and which entry spc takes is not this script's choice."
fi

cat "${patched}" >"${CRAFT_YML}"
echo "craft.yml now has spc clone libaom at ${LIBAOM_TAG}, under download-options:"
printf '%s\n' "${line}"

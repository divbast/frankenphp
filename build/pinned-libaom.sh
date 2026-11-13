#!/usr/bin/env bash
# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Fails the build unless spc fetched libaom at the AOM release LIBAOM_TAG
# pins, the build stage's ARG, whenever the build has libaom at all.
# SPC_CACHE names spc's download cache.
#
# libaom's own spc config clones its main branch's head. The Containerfile
# adds a custom-git line to craft.yml that clones the tag instead, and spc
# ignores such a line, without a word, when the name in it is not one it
# downloads. Its cache records a git clone's version as
# "dev-<ref>+<short commit>", so only a clone of the tag reads
# "dev-${LIBAOM_TAG}+". An app without gd, such as Baikal, has no libaom,
# and passes.
set -euo pipefail

CACHE="${SPC_CACHE:-dist/downloads/.cache.json}"
FAILED=0

fail() {
	echo "ERROR: ${1}" >&2
	FAILED=1
}

if [[ -z "${LIBAOM_TAG-}" ]]; then
	fail "LIBAOM_TAG is empty; the Containerfile must pin libaom to an AOM release tag."
fi
if [[ ! -s "${CACHE}" ]]; then
	fail "SPC_CACHE names no download cache: ${CACHE}"
fi
if ((FAILED)); then
	exit 1
fi

# spc's cache is one JSON object, keyed by each artifact it fetched. jq
# says above the ERROR line what it could not read.
if ! names=$(jq -r 'if type == "object" then keys[] else error("not one JSON object") end' "${CACHE}"); then
	fail "${CACHE} is not spc's download cache, or jq cannot read it."
	exit 1
fi
if ! grep -qxF libaom <<<"${names}"; then
	echo "no libaom in this build, so LIBAOM_TAG=${LIBAOM_TAG} pins nothing here"
	exit 0
fi

version=$(jq -r '.libaom.source.version // empty' "${CACHE}")
if [[ -z "${version}" ]]; then
	fail "spc recorded no version for libaom's source in ${CACHE}."
elif [[ "${version}" != "dev-${LIBAOM_TAG}+"* ]]; then
	fail "spc fetched libaom ${version}, but LIBAOM_TAG pins ${LIBAOM_TAG}: spc ignored the custom-git line the Containerfile adds to craft.yml."
fi

if ((FAILED)); then
	exit 1
fi
echo "libaom is ${version}, the AOM release LIBAOM_TAG pins"

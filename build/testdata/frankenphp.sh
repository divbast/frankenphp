#!/usr/bin/env bash
# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Stands in for the frankenphp binary in checks_test.go. Each subcommand the
# build scripts run prints its canned answer from the directory STUB_ANSWERS
# names, captured from a published binary and edited by the test.
set -euo pipefail

answers="${STUB_ANSWERS:?the directory of canned answers}"
case "${1-}" in
version | build-info)
	cat "${answers}/${1}"
	;;
php-cli)
	# php-cli -r CODE: gd's report is the code that asks gd_info()
	if [[ "${2-}" == -r && "${3-}" == *gd_info* ]]; then
		cat "${answers}/gd"
	elif [[ "${2-}" == -r && "${3-}" == *get_loaded_extensions* ]]; then
		cat "${answers}/extensions"
	else
		echo "stub frankenphp: no canned answer for php-cli ${*:2}" >&2
		exit 2
	fi
	;;
*)
	echo "stub frankenphp: no canned answer for: $*" >&2
	exit 2
	;;
esac

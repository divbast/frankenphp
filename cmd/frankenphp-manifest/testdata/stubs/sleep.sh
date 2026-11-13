#!/usr/bin/env bash
# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Stands in for sleep in publish_test.go: it records the call in STUB_LOG
# and returns at once, so the verify step's retries cost the test nothing.
set -euo pipefail

printf 'sleep %s\n' "$*" >>"${STUB_LOG:?the file the stubs record their calls in}"

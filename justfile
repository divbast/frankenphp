# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

set shell := ["mise", "exec", "--", "bash", "-euo", "pipefail", "-c"]

import? '.ci/just/common.just'

ENGINE := env_var_or_default("ENGINE", "docker")

# Tool versions live in mise.toml

default:
    @just --list

# Fetch divbast/ci at the commit the workflows pin, then install the toolchain
setup: ci-checkout
    #!/usr/bin/env bash
    # Not through the mise exec shell: this is what installs what it runs
    set -euo pipefail
    mise install

# Fetch divbast/ci at the commit the workflows pin, into .ci, and nothing else
ci-checkout:
    #!/usr/bin/env bash
    # Not through the mise exec shell, which would first install every tool
    # in mise.toml that is missing
    set -euo pipefail
    # The one release commit every workflow calls divbast/ci at
    uses="^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]*[\"']?divbast/ci/[^@[:space:]\"']+@[^[:space:]\"'#]+"
    sha=$(grep -hoE "${uses}" .github/workflows/*.y*ml | sed 's/.*@//' | sort -u | paste -sd ' ' - || true)
    if [[ ! "${sha}" =~ ^[0-9a-f]{40}$ ]]; then
        echo "ci-checkout: the workflows must call divbast/ci at one full commit SHA, not at: ${sha:-none}" >&2
        exit 1
    fi
    have=$(git -C .ci rev-parse HEAD 2>/dev/null || true)
    if [[ "${have}" == "${sha}" ]]; then
        echo "ci-checkout: .ci is already at ${sha}"
    else
        git init -q .ci
        git -C .ci fetch -q --depth 1 https://github.com/divbast/ci "${sha}"
        git -C .ci checkout -q FETCH_HEAD
    fi

# Build one app's carrier image, localhost/frankenphp:APP, from apps/APP.env
build-app APP:
    #!/usr/bin/env bash
    # One script, not a shell per line, so that the loop below can run
    set -euo pipefail
    # Every build script spc runs can read the token, so it must be a
    # fine-grained token with Public repositories (read-only) access (see
    # README), or CI's job token. Its prefix shows the kind, never the scope.
    case "${GITHUB_TOKEN-}" in
    github_pat_* | ghs_*) kind= ;;
    "") kind=empty ;;
    gho_*) kind="a gh/OAuth token (gho_)" ;;
    ghp_*) kind="a classic personal access token (ghp_)" ;;
    ghu_*) kind="a GitHub App user token (ghu_)" ;;
    *) kind="a token of no kind this knows" ;;
    esac
    if [[ -n "${kind}" ]]; then
        echo "build-app: GITHUB_TOKEN is ${kind}, not a fine-grained token (github_pat_) or CI's job token (ghs_); see README.md, \"A token for local builds\"" >&2
        exit 1
    fi
    # frankenphp-buildargs refuses anything but the four inputs, and prints
    # one build argument per line: a value may hold spaces, never a newline.
    out=$(mise exec -- go run ./cmd/frankenphp-buildargs {{ quote(APP) }})
    args=(--build-arg "APP="{{ quote(APP) }})
    while IFS= read -r arg; do
        args+=(--build-arg "${arg}")
    done <<<"${out}"
    # A secret, so no layer holds the token
    {{ ENGINE }} build --target carrier \
        --secret id=github_token,env=GITHUB_TOKEN "${args[@]}" \
        -t "localhost/frankenphp:"{{ quote(APP) }} -f Containerfile .

# Every lint gate and the Go tests: CI runs this recipe, so the tests gate
# every pull request
lint:
    {{ just_executable() }} actions-check workflow-audit actionlint shell-lint go-lint go-test reuse-lint
    hadolint Containerfile
    # BuildKit's own checks. --check is a buildx flag, so this needs Docker
    docker build --check -f Containerfile .

# The Go tests alone
test:
    {{ just_executable() }} go-test

# Every gate. Run before committing
ci: lint

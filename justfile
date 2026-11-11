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
    # frankenphp-buildargs refuses a file that sets anything but the four
    # inputs. It prints one whole build argument per line: a value may hold
    # spaces, never a newline.
    out=$(mise exec -- go run ./cmd/frankenphp-buildargs {{ quote(APP) }})
    args=(--build-arg "APP="{{ quote(APP) }})
    while IFS= read -r arg; do
        args+=(--build-arg "${arg}")
    done <<<"${out}"
    # gh's token reaches spc as a secret, so its GitHub API calls escape the
    # unauthenticated rate limit. It never reaches a layer.
    GITHUB_TOKEN="${GITHUB_TOKEN:-$(gh auth token)}" {{ ENGINE }} build --target carrier \
        --secret id=github_token,env=GITHUB_TOKEN "${args[@]}" \
        -t "localhost/frankenphp:"{{ quote(APP) }} -f Containerfile .

# Every lint gate
lint:
    {{ just_executable() }} actions-check workflow-audit actionlint shell-lint go-lint reuse-lint
    hadolint Containerfile
    # BuildKit's own checks. --check is a buildx flag, so this needs Docker
    docker build --check -f Containerfile .

# Every test
test:
    {{ just_executable() }} go-test

# Every gate. Run before committing
ci: lint test

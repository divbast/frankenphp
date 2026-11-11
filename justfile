# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

set shell := ["mise", "exec", "--", "bash", "-euo", "pipefail", "-c"]

import? '.ci/just/common.just'

ENGINE := env_var_or_default("ENGINE", "docker")

# Tool versions live in mise.toml

default:
    @just --list

# Fetch divbast/ci at the commit the workflows pin, then install the toolchain
setup:
    #!/usr/bin/env bash
    # Not through the mise exec shell: this is what installs what it runs
    set -euo pipefail
    # The one release commit every workflow calls divbast/ci at
    uses="^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]*[\"']?divbast/ci/[^@[:space:]\"']+@[^[:space:]\"'#]+"
    sha=$(grep -hoE "${uses}" .github/workflows/*.y*ml | sed 's/.*@//' | sort -u | paste -sd ' ' - || true)
    if [[ ! "${sha}" =~ ^[0-9a-f]{40}$ ]]; then
        echo "setup: the workflows must call divbast/ci at one full commit SHA, not at: ${sha:-none}" >&2
        exit 1
    fi
    have=$(git -C .ci rev-parse HEAD 2>/dev/null || true)
    if [[ "${have}" == "${sha}" ]]; then
        echo "setup: .ci is already at ${sha}"
    else
        git init -q .ci
        git -C .ci fetch -q --depth 1 https://github.com/divbast/ci "${sha}"
        git -C .ci checkout -q FETCH_HEAD
    fi
    mise install

# Build one app's carrier image, localhost/frankenphp:APP, from apps/APP.env
build-app APP:
    #!/usr/bin/env bash
    # Not through the mise exec shell: docker and gh are the host's own
    set -euo pipefail
    # Each KEY=value line, unquoted, is one build argument
    args=(--build-arg "APP={{ APP }}")
    while IFS= read -r line; do
        [[ -z "${line}" || "${line}" == "#"* ]] || args+=(--build-arg "${line}")
    done <"apps/{{ APP }}.env"
    # gh's token reaches spc as a secret, so its GitHub API calls escape the
    # unauthenticated rate limit. It never reaches a layer.
    GITHUB_TOKEN="${GITHUB_TOKEN:-$(gh auth token)}" {{ ENGINE }} build --target carrier \
        --secret id=github_token,env=GITHUB_TOKEN "${args[@]}" \
        -t "localhost/frankenphp:{{ APP }}" -f Containerfile .

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

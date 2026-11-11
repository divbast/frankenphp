# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# One static FrankenPHP binary per app, each in a carrier image that holds
# the binary and the records of how it was built. The pins below are every
# app's; what differs per app comes from apps/<app>.env, passed as build
# arguments with the app's name as APP.

ARG BUILDER_IMAGE=dunglas/frankenphp:static-builder-gnu@sha256:4dcdd53971ba1f373c3eb90e393dd3944687938c5c03494ab684921594905fc7
# The binary's own roots. Unset, build-static.sh takes the newest PHP 8.5 from
# php.net and xcaddy the newest Caddy and plugins, so two builds of one commit
# ship different binaries. build/pinned-versions.sh checks the binary agrees.
ARG PHP_VERSION=8.5.12
ARG CADDY_VERSION=v2.11.9
ARG CBROTLI_VERSION=v1.0.1
ARG MERCURE_VERSION=v1.1.6
ARG VULCAIN_VERSION=v1.4.6
# static-php-cli, which build-static.sh would otherwise download as an
# unchecked nightly binary on every build. Its v3 branch has no tags, so
# Renovate follows the branch head by commit. build/pinned-spc.sh checks it
# ran.
ARG SPC_COMMIT=ff04d4af9ba44f6c194239d733e0529cc6ddecea

FROM ${BUILDER_IMAGE} AS build
ARG SPC_COMMIT
# No WORKDIR: BuildKit stamps the directory one creates with
# SOURCE_DATE_EPOCH, which the build workflow sets to the commit time, and
# that gave this whole stage a new cache key on every commit. Every path
# below is absolute instead.
# spc from source at the pinned commit: git checks a fetch by commit against
# its hash, and composer installs from that commit's own lock file.
# build-static.sh skips its nightly download when dist/spc is executable, so
# the wrapper there runs this checkout instead, and records each subcommand
# for build/pinned-spc.sh. Both downloads get three retries, 10s, 30s and
# 90s apart, so one dropped connection does not cost the daily build.
RUN set -eux; \
    retry() { \
        for delay in 10 30 90; do "$@" && return 0; sleep "${delay}"; done; \
        "$@"; \
    }; \
    git init -q /opt/static-php-cli; \
    retry git -C /opt/static-php-cli fetch -q --depth 1 \
        https://github.com/crazywhalecc/static-php-cli.git "${SPC_COMMIT}"; \
    git -C /opt/static-php-cli checkout -q --detach FETCH_HEAD; \
    retry composer install --working-dir=/opt/static-php-cli \
        --no-dev --no-interaction --prefer-dist; \
    mkdir -p /go/src/app/dist; \
    printf '%s\n' '#!/bin/sh' \
        "echo \"\$1\" >>/go/src/app/dist/spc.calls" \
        "exec php /opt/static-php-cli/bin/spc \"\$@\"" >/go/src/app/dist/spc; \
    chmod 0755 /go/src/app/dist/spc
# Declared after the spc checkout, which every app shares: RUN sees each ARG
# as an environment variable, so each would otherwise give that layer a
# cache key per app. Every RUN below sees them, and the checks read their
# inputs from them.
ARG PHP_VERSION
ARG CADDY_VERSION
ARG CBROTLI_VERSION
ARG MERCURE_VERSION
ARG VULCAIN_VERSION
ARG APP
ARG PHP_EXTENSIONS
ARG PHP_EXTENSION_LIBS
ARG EXPECTED_EXTENSIONS
ARG REQUIRED_GD_FEATURES
# Left empty, build-static.sh would quietly build craft.yml's own extension
# and library lists. Fail now rather than after the long build.
RUN : "${APP:?is empty: pass apps/<app>.env as build arguments, as just build-app does}" \
    "${PHP_EXTENSIONS:?is empty: apps/<app>.env sets it}" \
    "${PHP_EXTENSION_LIBS:?is empty: apps/<app>.env sets it}" \
    "${EXPECTED_EXTENSIONS:?is empty: apps/<app>.env sets it}"
# The plugins go through XCADDY_ARGS, which build-static.sh writes into
# craft.yml. spc applies craft.yml's extra-env over the environment, so
# SPC_CMD_VAR_FRANKENPHP_XCADDY_MODULES set here would lose to its unversioned
# default and xcaddy would fetch the newest release of every plugin.
# PHP_EXTENSION_LIBS replaces craft.yml's extra libraries. craft.yml's
# with-suggests stays on, as upstream ships it. build-static.sh appends
# brotli, which caddy-cbrotli needs, whatever this says.
# spc asks the GitHub API which release of each library to fetch, and a call
# refused at the unauthenticated rate limit fails the build. The token comes
# as a secret, which no layer, history entry or cache key ever holds, as they
# would an ARG or ENV. Without the secret GITHUB_TOKEN is empty, and spc then
# sends no token at all.
RUN --mount=type=secret,id=github_token,env=GITHUB_TOKEN \
    PHP_EXTENSIONS="${PHP_EXTENSIONS}" \
    PHP_EXTENSION_LIBS="${PHP_EXTENSION_LIBS}" \
    PHP_VERSION="${PHP_VERSION}" \
    CADDY_VERSION="${CADDY_VERSION}" \
    XCADDY_ARGS="--with github.com/dunglas/caddy-cbrotli@${CBROTLI_VERSION} --with github.com/dunglas/mercure/caddy@${MERCURE_VERSION} --with github.com/dunglas/vulcain/caddy@${VULCAIN_VERSION}" \
    /go/src/app/build-static.sh
RUN cp "$(ls /go/src/app/dist/frankenphp-linux-*)" /go/src/app/dist/frankenphp
# Each check is copied in after the build, so that changing one never
# rebuilds the binary.
COPY --chmod=0755 build/pinned-versions.sh /tmp/pinned-versions.sh
RUN FRANKENPHP_BIN=/go/src/app/dist/frankenphp /tmp/pinned-versions.sh
COPY --chmod=0755 build/pinned-spc.sh /tmp/pinned-spc.sh
RUN SPC_SRC=/opt/static-php-cli \
    SPC_WRAPPER=/go/src/app/dist/spc \
    SPC_CALLS=/go/src/app/dist/spc.calls \
    SPC_CLONE=/go/src/app/dist/static-php-cli \
    /tmp/pinned-spc.sh
COPY --chmod=0755 build/extensions.sh /tmp/extensions.sh
RUN FRANKENPHP_BIN=/go/src/app/dist/frankenphp /tmp/extensions.sh

FROM build AS records
ARG APP
COPY --chmod=0755 build/records.sh /tmp/records.sh
RUN FRANKENPHP_BIN=/go/src/app/dist/frankenphp \
    SPC_CACHE=/go/src/app/dist/downloads/.cache.json \
    BUILD_MANIFEST=/go/src/app/dist/buildroot/build-manifest.json \
    RECORDS=/records \
    /tmp/records.sh

# Not runnable: the binary links glibc dynamically, and scratch has none. An
# app copies /frankenphp into its own image by digest.
FROM scratch AS carrier
ARG APP
COPY --from=build --chmod=0755 /go/src/app/dist/frankenphp /frankenphp
COPY --from=records /records/ /records/
LABEL org.opencontainers.image.source="https://github.com/divbast/frankenphp" \
    org.opencontainers.image.description="The FrankenPHP binary for divbast's ${APP} image, and the records of how it was built"

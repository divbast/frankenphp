# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# One static FrankenPHP binary per app, each in a carrier image that holds
# the binary and the records of how it was built. The pins below are every
# app's; what differs per app comes from apps/<app>.env, passed as build
# arguments with the app's name as APP.

ARG BUILDER_IMAGE=dunglas/frankenphp:static-builder-gnu@sha256:4dcdd53971ba1f373c3eb90e393dd3944687938c5c03494ab684921594905fc7
# The binary's own roots. Unset, build-static.sh takes the newest PHP 8.5 from
# php.net and xcaddy the newest Caddy and plugins, so two builds of one commit
# could ship a different PHP or Caddy. build/pinned-versions.sh checks the
# binary agrees. Not every input is pinned: spc still chooses the Go
# toolchain and most C libraries as it runs, and records.sh writes down
# which.
ARG PHP_VERSION=8.5.12
ARG CADDY_VERSION=v2.11.9
ARG CBROTLI_VERSION=v1.0.1
ARG MERCURE_VERSION=v1.1.6
ARG VULCAIN_VERSION=v1.4.6
# static-php-cli, which build-static.sh would otherwise download as an
# unchecked nightly binary on every build. Its v3 branch has no tags, so
# Renovate follows the branch head by commit. build/pinned-spc.sh checks it
# ran.
ARG SPC_COMMIT=3ba9ffa0593b94202b48d6dde51e489191ecdbf1
# xcaddy, which writes the binary's main module and runs its build. spc
# would otherwise install it from its master branch on most builds, as the
# build stage below says. build/pinned-versions.sh checks the xcaddy that
# ran.
ARG XCADDY_VERSION=v0.4.7
# libaom, which LibreSpeed's gd brings in through libavif. spc's own config
# clones the head of its main branch, which moves most days. An AOM release
# tag, never a commit: spc clones it with `git clone --branch`.
# build/pinned-libaom.sh checks what spc cloned.
ARG LIBAOM_TAG=v3.15.3

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
# Every app's, and each declared just before the step that reads it, so
# that moving one pin reruns only what follows it.
ARG XCADDY_VERSION
# spc's go-xcaddy package is the newest Go go.dev lists and an xcaddy. spc
# unpacks it into PKG_ROOT_PATH whenever that Go is not the one already
# there, which is most builds, and each time installs xcaddy from its
# master branch. So unpack it here, into the download cache and package
# root the craft run reuses, and install XCADDY_VERSION over the xcaddy spc
# built. The craft run then finds the package unpacked from the same
# download (its .spc-hash matches) and leaves it alone. spc's master build
# here must still compile, though nothing runs it. env -C runs spc from
# dist/, where build-static.sh runs it, without a WORKDIR (see above).
# PKG_ROOT_PATH is the builder image's, which spc reads too, so every path
# to the package below follows it. Neither step calls the GitHub API, so
# neither sees the token.
RUN set -eux; \
    : "${PKG_ROOT_PATH:?is unset: spc unpacks go-xcaddy under it, and the builder image sets it}"; \
    retry() { \
        for delay in 10 30 90; do "$@" && return 0; sleep "${delay}"; done; \
        "$@"; \
    }; \
    retry env -C /go/src/app/dist \
        /go/src/app/dist/spc install-pkg go-xcaddy --dl-retry=5; \
    retry env CGO_ENABLED=0 GOTOOLCHAIN=local \
        GOROOT="${PKG_ROOT_PATH}/go-xcaddy" \
        GOBIN="${PKG_ROOT_PATH}/go-xcaddy/bin" \
        GOPATH="${PKG_ROOT_PATH}/go-xcaddy/go" \
        "${PKG_ROOT_PATH}/go-xcaddy/bin/go" install \
        "github.com/caddyserver/xcaddy/cmd/xcaddy@${XCADDY_VERSION}"
ARG LIBAOM_TAG
# spc reads download-options from craft.yml, and build-static.sh copies
# every line it does not rewrite, so a custom-git line there makes spc clone
# libaom at LIBAOM_TAG. spc ignores such a line, without a word, when no
# package it downloads has that name: build/pinned-libaom.sh checks the
# clone after the build. The checks here catch a sed that matched nothing,
# and a craft.yml that grew a custom-git line of its own.
RUN set -eux; \
    line="  custom-git: [\"libaom:${LIBAOM_TAG}:https://aomedia.googlesource.com/aom\"]"; \
    sed -i "/^download-options:\$/a\\${line}" /go/src/app/craft.yml; \
    grep -qxF "${line}" /go/src/app/craft.yml; \
    test "$(grep -c 'custom-git:' /go/src/app/craft.yml)" -eq 1
# Declared after the steps every app shares: RUN sees each ARG as an
# environment variable, so each would otherwise give those layers a cache
# key per app. Every RUN below sees them, and the checks read their inputs
# from them.
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
# would an ARG or ENV. It is in the environment of this whole RUN, though,
# and spc passes its environment to everything it starts, so every library's
# build scripts and the Go build can read it. CI passes its job's read-only
# token; a local build takes a fine-grained token with Public repositories
# (read-only) access; see README.md, "A token for local builds".
# Without the secret GITHUB_TOKEN is empty, and spc then sends no token at
# all.
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
RUN FRANKENPHP_BIN=/go/src/app/dist/frankenphp \
    GO_BIN="${PKG_ROOT_PATH}/go-xcaddy/bin/go" \
    XCADDY_BIN="${PKG_ROOT_PATH}/go-xcaddy/bin/xcaddy" \
    /tmp/pinned-versions.sh
COPY --chmod=0755 build/pinned-libaom.sh /tmp/pinned-libaom.sh
RUN SPC_CACHE=/go/src/app/dist/downloads/.cache.json /tmp/pinned-libaom.sh
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
# What built the binary without the binary saying so, which records.sh
# reads from the environment. RECIPE_SHA256 is the sha256 of this file and
# apps/<app>.env, which build.yml and just build-app compute and pass. It
# is declared here and nowhere in the build stage: any edit to either file
# gives it a new value, and an ARG puts its value in the cache key of every
# step after it, so in the build stage a comment would rebuild the binary.
# Here it reruns only records.sh.
ARG BUILDER_IMAGE
ARG SPC_COMMIT
ARG RECIPE_SHA256
COPY --chmod=0755 build/records.sh /tmp/records.sh
# go-xcaddy is spc's package of the Go toolchain and the xcaddy that built
# the binary, under the builder's PKG_ROOT_PATH. build-static.sh renders
# craft.yml into dist/ before spc reads it.
RUN FRANKENPHP_BIN=/go/src/app/dist/frankenphp \
    SPC_CACHE=/go/src/app/dist/downloads/.cache.json \
    BUILD_MANIFEST=/go/src/app/dist/buildroot/build-manifest.json \
    CRAFT=/go/src/app/dist/craft.yml \
    GO_BIN="${PKG_ROOT_PATH}/go-xcaddy/bin/go" \
    XCADDY_BIN="${PKG_ROOT_PATH}/go-xcaddy/bin/xcaddy" \
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

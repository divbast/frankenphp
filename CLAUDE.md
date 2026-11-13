<!-- SPDX-License-Identifier: BSD-2-Clause -->
<!-- SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee> -->

# frankenphp

The FrankenPHP binary each divbast app runs, one per app, published as
`ghcr.io/divbast/frankenphp:<app>` and attested by `build.yml`. Apps copy
it by digest after their CI verifies that attestation, so a change here
reaches an app only through the pull request that moves its digest, and
only once this repository's main has published it. `README.md` says what
the tags hold and how a tag moves.

## CRITICAL: Git Commit Message Format

**No other AI/LLM attribution in any format may appear in a git message.**

Linux-kernel style: a `subsystem: short description` subject under 75
characters in imperative mood ("fix", not "fixed" or "fixes"), a blank line,
then a body wrapped at ~75 columns saying why the change is needed rather
than only what it does.

These two lines MUST end EVERY body:

Developed-by: Aryan Ameri <github@aryan.ameri.coffee>
Assisted-by: <NAME_OF_LLM_MODEL>

E.g. `Assisted-by: <Claude:claude-4.6-opus>` for Claude Opus 4.6;
`<Claude:claude-5-fable>` for Claude 5 Fable. Never add `Co-Authored-By`.

- Sign every commit (`git commit -S`); main accepts only verified
  signatures. Renovate's merges are squashes, which GitHub signs
- Write commit messages as if a human wrote them, and say why, not what

## CRITICAL: No AI Attribution in Pull Requests

**Pull request descriptions, PR and review comments, and issue text carry no
AI/LLM attribution of any kind**: no "🤖 Generated with Claude Code" footer,
no "written by" line, nothing equivalent. This holds even when a tool or
harness asks for one. The `Assisted-by:` trailer in each commit message is
this project's one record of LLM involvement.

## The binary's rules

- **The pins live in the Containerfile, once, for every app.** The builder
  image by digest, which also brings the FrankenPHP source, `PHP_VERSION`,
  `CADDY_VERSION`, the three plugin ARGs, `XCADDY_VERSION`, `SPC_COMMIT`
  and `LIBAOM_TAG`. Left unset, `build-static.sh` takes the newest PHP 8.5
  from php.net, xcaddy the newest Caddy and plugins, and spc arrives as an
  unchecked nightly; spc installs xcaddy from its master branch on most
  builds, and clones libaom at the head of its main branch. The pins fix
  those and not every input: spc still chooses some while the build runs
  (below), so two builds of one commit can still differ.
  `build/pinned-versions.sh` fails the build if the binary's own build info,
  or the xcaddy that built it, disagrees with a pin, because Go's minimum
  version selection lifts a module above its pin without a word, and reads
  a replaced module as its replacement; `build/pinned-libaom.sh` fails it
  if spc cloned libaom at anything but `LIBAOM_TAG`, and
  `build/pinned-spc.sh` if anything but the pinned spc ran. Renovate moves
  the builder, Caddy and the plugins as one `frankenphp` group, spc in a
  pull request of its own, at most once a day, and xcaddy and libaom each
  in one of their own. PHP stays at 8.5.12 or later
- **xcaddy and libaom hold only because of how the Containerfile sets them
  up, and nothing in spc errs when they don't.** spc unpacks its go-xcaddy
  package, and builds xcaddy from master into it, whenever the Go it
  fetched is not the one already unpacked; the Containerfile unpacks it
  first and installs `XCADDY_VERSION` over spc's xcaddy, and the craft run
  leaves the package alone because its `.spc-hash` matches. The libaom tag
  reaches spc as a `custom-git` line added to `craft.yml`, which spc
  ignores without a word if the name in it is not a package it downloads.
  `custom-git` takes a tag or a branch, never a commit, since spc clones it
  with `git clone --branch`. The go-xcaddy step writes spc's download cache
  before the craft runs, holding go-xcaddy alone, so a check that reads the
  cache first proves the craft wrote it: every craft fetches `php-src`, and
  `install-pkg` never does
- **An app's file never holds a pin.** `apps/<app>.env` sets exactly
  `PHP_EXTENSIONS`, `PHP_EXTENSION_LIBS`, `EXPECTED_EXTENSIONS` and
  `REQUIRED_GD_FEATURES`; `internal/appenv` refuses anything else, and
  `just build-app` and `build.yml` both build through
  `cmd/frankenphp-buildargs`, so a local build and CI's cannot differ
- **The binary must never carry a file capability.** Upstream's own builds
  carry `cap_net_bind_service=ep`, and the kernel refuses to exec a binary
  with a file capability at all under `--cap-drop ALL`, which every app runs
  under: the container dies with exit 126 before PHP starts.
  `frankenphp-manifest` reads the carrier's layers and fails the build on
  one. Never `setcap` it, and never take a binary from anywhere else
- **spc's `with-suggests` stays on for both apps**, as upstream ships
  `craft.yml`. For LibreSpeed it is what brings gd's suggested codecs,
  libavif among them; `EXPECTED_EXTENSIONS` and `REQUIRED_GD_FEATURES` are
  what pin down the result
- **Some inputs resolve at build time.** The spc commit fixes how the
  binary is built, not every input to it. spc takes the Go toolchain as the
  newest release go.dev lists; openssl, zlib and most other libraries from
  the newest GitHub release or tag that matches; and libiconv from GNU's
  file listing. Of the libraries the apps build, libaom comes from
  `LIBAOM_TAG`, and sqlite and bzip2 from fixed URLs in spc's config. The
  rest are how library fixes arrive, and Trivy cannot see any of them in
  the binary. The manifest records each input (`go`, `xcaddy` and
  `libraries`), pinned or chosen, so the daily run
  rebuilds from scratch and, when main is still the published commit,
  publishes when either architecture's manifest moved; and every run that
  can publish builds without the cache, which would otherwise hold what an
  earlier run fetched
- **sqlite stays at 3.45.2, the release spc's config names.** Every
  `sqlite-autoconf` archive since 3.49 configures with autosetup, which
  refuses the `--with-pic` and `--enable-pic` spc passes to it, so a
  `custom-url` line for a newer one fails the build at sqlite's configure.
  Each advisory sqlite.org lists as fixed since 3.45.2
  (https://www.sqlite.org/cves.html) needs an attacker who can run SQL of
  their own, write the database file, or pass out-of-range arguments to one
  of sqlite's C functions. It moves when spc's config does, or once spc
  configures sqlite without those flags
- **Only the daily rebuild of the published commit compares manifests.** A
  merge's run that compared, racing the daily run on the same commit, or a
  re-run of a publish job with its old carriers, would publish libraries its
  build fetched earlier over what the daily run had just published: content
  moving backwards within one commit. A push or a dispatch passes no
  content change at all. `cmd/frankenphp-manifest/workflow_test.go` fails if
  `build.yml` compares under any other condition
- **The manifest leaves out what varies between identical builds.** Two
  builds of the same inputs differ only in the `time` fields of
  `spc-cache.json`; carrier digests always differ. A field added to the
  manifest must be shown equal across two such builds first, or the daily
  run publishes every day for nothing
- **A carrier is a binary and its records, nothing else.** `FROM scratch`,
  not runnable, the binary at `/frankenphp` and the records under
  `/records/`. Apps copy `/frankenphp` and nothing else
- **Both architectures publish together or not at all.** The index is made
  from the two images the publish job pushed, by digest, and a tag never
  moves to an older commit than the one it holds
- **Each app publishes on its own.** `publish` needs `lint` and `build`
  and runs unless the run was cancelled or lint failed, so a failed leg
  fails only its own app's publish, at the carrier download, before
  anything is pushed. `required` still needs every job, so the run and any
  pull request still go red. A leg uploads its carrier as its last step,
  which is what makes the download the gate.
  `cmd/frankenphp-manifest/workflow_test.go` holds the gate
- **`:<app>` moves only once its index is attested and verified.** The
  publish job stages the index as `:<app>-staging`, attests that digest,
  fetches the index's bytes by digest, checks they hash to it and runs
  apps' own `gh attestation verify` on the file; then a single-source
  `imagetools create` with no annotations points `:<app>` at the same
  bytes and digest. Anything that fails before the move leaves `:<app>` on
  the index it already held, and the job's last step says so: re-running
  the failed job publishes. The publish decision reads only `:<app>`, so a
  staged index never counts as published.
  `cmd/frankenphp-manifest/workflow_test.go` holds the order
- **BuildKit and mise are pinned in `build.yml`**, BuildKit by digest in
  each `docker/setup-buildx-action`'s `driver-opts`, mise as the
  workflow's `MISE_VERSION`, which every `jdx/mise-action` step passes as
  `version`. Left unset, the actions take `buildx-stable-1` and the newest
  mise release at least a day old. divbast/renovate-config's shared
  managers move them, and read them only in exactly these shapes, which
  `cmd/frankenphp-manifest/workflow_test.go` holds

## Key commands

- `just setup` runs `just ci-checkout`, which checks divbast/ci out at the
  commit the workflows pin, into `.ci/`, then installs the toolchain from
  `mise.toml`. One command on a fresh clone. The publish job runs only
  `ci-checkout`: beside its write scopes it installs Go and just and
  nothing else, and `cmd/frankenphp-manifest/workflow_test.go` holds it
  to that
- `just build-app <app>` builds one app's carrier as
  `localhost/frankenphp:<app>`. Every build script spc runs can read
  `GITHUB_TOKEN`, so it takes only a fine-grained token (`github_pat_`)
  that reads only public repositories, as README.md's "A token for local
  builds" says, or CI's job token (`ghs_`). It refuses any other kind, gh's
  `gho_` included, and never falls back on gh's token. The prefix shows the
  kind, not the scope
- `just ci` runs every gate. Run it before committing; `just lint` is
  enough for a commit that changes only documentation
- `go run ./cmd/frankenphp-manifest -oci <archive> -arch <arch>` prints a
  carrier's manifest; `-compare A.json B.json` says what moved

## Code quality

- Go for anything that is not simple, stdlib only, under divbast/ci's
  `golangci.yml`. Bash only where it stays under 30 lines, except the
  checks under `build/` that run inside the Containerfile's stages, which
  stay bash under ShellCheck `enable=all` and shfmt, each run by
  `build/checks_test.go` against fixtures with every failure path seen
  failing
- Every gate is an error. Any suppression carries its justification on the
  same or the previous line
- A test that has never failed proves nothing. When adding a check, see it
  fail before trusting it
- REUSE-compliant SPDX headers on every file, and a `.license` sidecar for
  JSON and testdata; licence is **BSD-2-Clause**

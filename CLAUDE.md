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
  image by digest, `PHP_VERSION`, `CADDY_VERSION`, the three plugin ARGs and
  `SPC_COMMIT`. Left unset, `build-static.sh` takes the newest PHP 8.5 from
  php.net, xcaddy the newest Caddy and plugins, and spc arrives as an
  unchecked nightly, so two builds of one commit would differ.
  `build/pinned-versions.sh` fails the build if the binary's own build info
  disagrees with a pin, because Go's minimum version selection lifts a
  module above its pin without a word, and `build/pinned-spc.sh` fails it if
  anything but the pinned spc ran. Renovate moves the builder, Caddy and the
  plugins as one `frankenphp` group, and spc in a pull request of its own,
  at most once a day. PHP stays at 8.5.12 or later
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
- **Library sources resolve at build time.** The spc commit fixes how the
  binary is built, not every source in it: openssl, zlib and several other C
  libraries come from the newest release that matches, and libaom from its
  main branch's head. That is how C-library fixes arrive, and Trivy cannot
  see those libraries in the binary. So the daily run rebuilds from scratch
  and, when main is still the published commit, publishes when either
  architecture's manifest moved; and every run that can publish builds
  without the cache, which would otherwise hold what an earlier run fetched
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

## Key commands

- `just setup` runs `just ci-checkout`, which checks divbast/ci out at the
  commit the workflows pin, into `.ci/`, then installs the toolchain from
  `mise.toml`. One command on a fresh clone. The publish job runs only
  `ci-checkout`: beside its write scopes it installs Go and just and
  nothing else, and `cmd/frankenphp-manifest/workflow_test.go` holds it
  to that
- `just build-app <app>` builds one app's carrier as
  `localhost/frankenphp:<app>`
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

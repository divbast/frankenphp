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
  `LIBAOM_TAG`, and sqlite and bzip2 from fixed URLs in spc's config;
  bzip2's config also names a `source-mirror`, sourceware's listing, which
  spc uses only if the primary URL fails, and takes the newest bzip2 there.
  The floating libraries are listed per app in README.md, read from the
  published carriers' `spc-cache.json` on 2026-11-13 (spc `3ba9ffa`), and
  nothing checks that list; regenerate it from a carrier's `/records` when
  spc's pin or an app's extensions move. The floating libraries are how
  library fixes arrive, and Trivy cannot see any of them in the binary.
  The manifest records each input (`go`, `xcaddy` and
  `libraries`), pinned or chosen, so the daily run rebuilds from scratch
  and publishes when either architecture's manifest moved; and every run
  that can publish builds without the cache, which would otherwise hold
  what an earlier run fetched
- **sqlite stays at 3.45.2, the release spc's config names.** Every
  `sqlite-autoconf` archive since 3.49 configures with autosetup, which
  refuses the `--with-pic` and `--enable-pic` spc passes to it, so a
  `custom-url` line for a newer one fails the build at sqlite's configure.
  It moves when spc's config does, or once spc configures sqlite without
  those flags
- **A merge and the daily run publish only when the content moved.** Each
  compares both architectures' manifests with the published ones and
  passes `divbast-publish-decision -content-gated`, so a merge that leaves
  the binary as it was moves no tag and makes no digest pull request in
  any app; with nothing published they publish, and the button always
  does. A merge's run compares only once main has moved past the published
  commit: on that commit a content change publishes whatever the event,
  so a merge's run racing the daily run, or a re-run of a publish job with
  its old carriers, would publish libraries its build fetched earlier over
  what the daily run had just published, content moving backwards within
  one commit. The button compares nothing. The flag goes only with the
  compare, so nothing is gated on a compare that did not run.
  `cmd/frankenphp-manifest/workflow_test.go` pins the condition and runs
  the decide step for every event against stubs. `stall.yml`'s slots jobs
  pass `content-gated: true`, without which a successful slot that left an
  older commit published counts as lost, and `slot-job: publish (<app>)`,
  which reads that app's publish job in the slot run, not the whole run's
  conclusion, so a leg that failed for the other app is not this app's
  lost slot
- **The manifest holds how the binary was built, not only what it holds.**
  `spc`, `builder`, `craft` and `recipe` (schema 3) catch what the binary
  does not report: an spc bump, a builder bump with the same FrankenPHP,
  and the flags in the rendered `dist/craft.yml`. `recipe` is the sha256
  of the `Containerfile` then `apps/<app>.env`, which `build.yml` and
  `just build-app` compute alike and pass as `RECIPE_SHA256`. Only the
  records stage declares that ARG: an ARG puts its value in the cache key
  of every step after it, so in the build stage any edit to either file,
  a comment among them, would rebuild the binary. `build/containerfile_test.go`
  holds it there
- **A new schema publishes once.** Each schema adds records
  (`schemaRecords` in `internal/manifest`), and a published carrier whose
  records do not read as the current schema is a change, reported as
  `published manifest unreadable (schema N)` with N the newest schema its
  records complete, so the merge that bumps the schema publishes both
  carriers. A candidate whose own records cannot be read fails the build.
  A schema change must add a record, or N cannot tell the old from the new
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
  fetches the index's bytes anonymously by digest, checks they hash to it
  and runs apps' own `gh attestation verify` on the file, with three
  tries. A `--dry-run` of the move must then show those same bytes before
  a single-source `imagetools create` with no annotations points `:<app>`
  at them. Every step up to the move runs only when the decision says
  publish, and no step may `continue-on-error`. Anything that fails before
  the move leaves `:<app>` on the index it already held, and the job's
  last step says so: re-running the failed job publishes while the build
  legs' carriers are kept (`retention-days: 1`), and after that the
  button does at once and the next daily or push run does if its content
  moved, unless the failing step names its own remedy: a
  failed anonymous fetch points at the package's settings, and a dry run
  showing another digest says a re-run meets the same. The publish
  decision reads only `:<app>`, so a staged index never counts as
  published. The job's permissions stay `contents: read` and `packages`,
  `id-token` and `attestations: write`; `created` comes from the clock,
  not the run's API record, which would take `actions: read`.
  `cmd/frankenphp-manifest/workflow_test.go` holds the order, the `if:`
  gates and the permissions
- **The publish job's shell is pinned line for line.** Every `run:` block
  of the publish job must equal
  `cmd/frankenphp-manifest/testdata/publish-shell.txt`, because no check
  that reads the shell for writes to `:<app>` survives one written another
  way. So must what shapes how bash runs it: each step's `env:`, the job's
  keys, `env:` and `defaults:`, and the workflow's keys, `defaults:` and
  the names of its `env:`, whose `MISE_VERSION` value Renovate moves. An
  env entry such as `BASH_ENV` runs before any script, and a `PATH` picks
  every command. `TestPublishShellIsBashWithPipefail` holds the job's
  effective shell to `bash`, `--noprofile --norc -eo pipefail`, which the
  behaviour tests run the steps under; left unset, Actions runs
  `bash -e` without pipefail. A step written in any YAML form but the
  plain one the extractor reads, such as `run: |-`, a flow mapping or a
  `shell:` key, fails the test rather than going unread. A change shows as
  a diff: read it, and only if it is meant run
  `go test ./cmd/frankenphp-manifest/ -run '^TestPublishShellPinned$' -update`
  and commit the file with it. `publish_test.go` also runs the verify and
  dry-run steps' own scripts under bash against the stubs in
  `testdata/stubs`, so a lost `exit 1` fails a test, not a publish
- **BuildKit and mise are pinned in `build.yml`**, BuildKit by digest in
  each `docker/setup-buildx-action`'s `driver-opts`, mise as the
  workflow's `MISE_VERSION`, which every `jdx/mise-action` step passes as
  `version`. Left unset, the actions take `buildx-stable-1` and the newest
  mise release at least a day old. divbast/renovate-config's shared
  managers move them, and read them only in exactly these shapes, which
  `cmd/frankenphp-manifest/workflow_test.go` holds
- **The publish job's steps get none of `mise.toml`'s `[env]`, PATH or
  shims.** Its `jdx/mise-action` step sets `env`, `export_path` and
  `add_shims_to_path` to `"false"`, since each defaults to true and would
  write them into every later step's shell, where a `BASH_ENV` or a `PATH`
  would change what the pinned scripts run. `go` and `just` run as
  `mise exec -- go` and `mise exec -- just`, and only a command run that
  way gets the toolchain and the `[env]`: the bash `just` starts reads a
  `BASH_ENV` set there, so `mise.toml`'s `[env]` stays free of one, and
  `TestMiseEnvHoldsNoBashStartupKey` fails on `BASH_ENV`, `ENV` or a
  `BASH_FUNC_*` key in it. The job's
  `uses:` steps are exactly the actions the test lists, in order, each
  pinned to a full SHA with its version as a comment: an action it gains
  is a change to review in that test. This is divbast/ci's rule for its own
  publish job

## Key commands

- `just setup` runs `just ci-checkout`, which checks divbast/ci out at the
  commit the workflows pin, into `.ci/`, then installs the toolchain from
  `mise.toml`. One command on a fresh clone. The publish job runs only
  `ci-checkout`: beside its write scopes it installs Go and just and
  nothing else, and `cmd/frankenphp-manifest/workflow_test.go` holds it
  to that
- `just build-app <app>` builds one app's carrier as
  `localhost/frankenphp:<app>`. Every build script spc runs can read
  `GITHUB_TOKEN`, so it takes only a fine-grained token (`github_pat_`) that
  reads only public repositories, as README.md's "A token for local builds"
  says, or CI's job token (`ghs_`). divbast/ci's shared `_github-token`
  recipe, which `build-app` runs first, refuses any other kind, gh's `gho_`
  included, and nothing falls back on gh's token. The prefix shows the kind,
  not the scope
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

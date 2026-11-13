<!-- SPDX-License-Identifier: BSD-2-Clause -->
<!-- SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee> -->

# frankenphp

The [FrankenPHP](https://github.com/php/frankenphp) binaries divbast's
images run: PHP 8.5 and Caddy in one binary, built once per app with that
app's PHP extensions, and attested. The builder image, FrankenPHP, PHP,
Caddy and its plugins, xcaddy, static-php-cli and libaom are pinned; the Go
toolchain and most of the C libraries are chosen while the build runs, and
each carrier records what was chosen, as
[What goes into a binary](#what-goes-into-a-binary) says.

Published as `ghcr.io/divbast/frankenphp`.

## Tags

| Tag | What it is |
|---|---|
| `:baikal` | Baikal's binary, for amd64 and arm64 |
| `:librespeed` | LibreSpeed's binary, for amd64 and arm64 |
| `:<app>-amd64`, `:<app>-arm64` | what each publish pushes first, to make the index above from. Not for use |
| `:<app>-staging` | the index each publish attests and verifies before it moves `:<app>` to it. Not for use |

Each tag is a carrier, not a runnable image: built `FROM scratch`, it holds
the binary at `/frankenphp` and the records of how it was built under
`/records/`. The binary links glibc dynamically, so it runs on the app's own
base, such as `gcr.io/distroless/cc-debian13`, never on scratch. It carries
no file capability, so it runs under `--cap-drop ALL`.

Each index's `org.opencontainers.image.revision` annotation is the commit
it was built from, and `org.opencontainers.image.created` the time its
publish job made it, which for a daily republish of an unchanged commit is
later than the commit. The revision can be older than main: a merge that
changes nothing in the binary publishes nothing, as
[When a tag moves](#when-a-tag-moves) says.

## Using a binary

An app's Containerfile copies the binary in, naming its tag by digest so
that Renovate can move it:

```dockerfile
COPY --from=ghcr.io/divbast/frankenphp:librespeed@sha256:<digest> --chmod=0755 /frankenphp /usr/local/bin/frankenphp
```

The digest is the tag's index, which covers both architectures. Before the
image builds, the app's CI verifies that `build.yml` on this repository's
main branch attested that digest (`divbast-verify-frankenphp`, in
[divbast/ci](https://github.com/divbast/ci)), so neither a pull request's
build nor another repository's image reaches an app.

To verify a tag yourself, with gh 2.102.0 or newer:

```sh
mkdir -p /tmp/empty-docker-config
DOCKER_CONFIG=/tmp/empty-docker-config \
gh attestation verify oci://ghcr.io/divbast/frankenphp:<app> --repo divbast/frankenphp --signer-workflow divbast/frankenphp/.github/workflows/build.yml@refs/heads/main --source-ref refs/heads/main
```

`DOCKER_CONFIG` points gh at an empty directory: it would otherwise send
whatever registry login Docker holds, and GHCR refuses a stale one even for
a public image. Name a digest, `oci://ghcr.io/divbast/frankenphp@sha256:<digest>`,
to verify what an app's Containerfile pins.

## What goes into a binary

Every app's binary is built by one `Containerfile` from the same pins:

- the `dunglas/frankenphp:static-builder-gnu` image, by digest, which also
  holds the FrankenPHP source the binary is built from;
- PHP, Caddy and Caddy's three plugins, by version;
- xcaddy, which writes the binary's main module and runs its build, by
  version;
- static-php-cli (spc), which builds PHP and its C libraries, by commit;
- libaom, the AV1 codec LibreSpeed's gd brings in through libavif, by AOM
  release tag.

`build/pinned-versions.sh`, `build/pinned-libaom.sh` and
`build/pinned-spc.sh` fail the build if the binary or the xcaddy that built
it disagrees with a pin, if spc cloned libaom at anything but the tag, or
if anything but the pinned spc ran.

spc would otherwise install xcaddy from its master branch on most builds,
whenever it unpacks a newer Go than the builder image holds, and clone
libaom at the head of its main branch. So the Containerfile unpacks spc's
Go package before the build and installs the pinned xcaddy over the one spc
built there, which spc then leaves alone, and adds a line to `craft.yml`
that tells spc to clone libaom at the tag.

What differs per app lives in `apps/<app>.env`: the PHP extensions and extra
libraries to build, and the exact list of modules the binary must load,
which `build/extensions.sh` checks. `cmd/frankenphp-buildargs` reads the file
and refuses anything else in it; `just build-app` and the build workflow
both build through it.

The pins do not fix every input. spc chooses these while the build runs:

- the Go toolchain: the newest release go.dev lists;
- the C libraries each app builds that spc takes from the newest GitHub
  release or tag that matches its rule for each, or from GNU's file
  listing for libiconv:
  - Baikal: brotli, libiconv, libxml2, onig, openssl, watcher, xz and
    zlib;
  - LibreSpeed: brotli, freetype, libavif, libiconv, libjpeg, libpng,
    libwebp, libxml2, openssl, watcher, xz and zlib.

Of the libraries the two apps build, libaom (LibreSpeed's) comes from the
tag above instead, and sqlite (both) and bzip2 (LibreSpeed's) from fixed
URLs in spc's config. bzip2's config also names a `source-mirror`, sourceware's
directory listing, which spc falls back on only when the primary URL fails;
the mirror takes the newest `bzip2-*.tar.gz` it lists, so a bzip2 build that
quietly changed version would mean the primary URL failed. A rebuild of an
unchanged commit can therefore hold newer code in the floating libraries,
which is how their fixes arrive. The manifest below records each of these
inputs, so the daily rebuild publishes when one moves.

These lists were read on 2026-11-13 from the published carriers'
`spc-cache.json` records and, for bzip2's mirror, spc's config at the
pinned commit `3ba9ffa`. Nothing checks them: they change when spc's
config or an app's extensions do, and are regenerated from a carrier's
records (`docker create` the carrier, `docker cp` its `/records`, and read
each library's `config.type` in `spc-cache.json`: `ghrel`, `ghtar`,
`ghtagtar` and `filelist` float; `url` and `git` with a tag are fixed).

sqlite stays at 3.45.2, the release spc's config names. Every
`sqlite-autoconf` archive since 3.49 configures with autosetup, which
refuses the `--with-pic` and `--enable-pic` that spc passes to every
library it configures this way, so pointing spc at a newer one fails the
build at sqlite's configure. Each advisory
[sqlite.org lists](https://www.sqlite.org/cves.html) as fixed since 3.45.2
needs an attacker who can run SQL of their own, write the database file,
or pass out-of-range arguments to one of sqlite's C functions; in Baikal
or LibreSpeed the first two take a SQL injection in the app, or write
access to its data, first.

## The manifest

`build/records.sh` writes into the carrier what the binary reports about
itself, what Go reports about the xcaddy that built it and what spc
recorded: `app.txt`, `version.txt`, `build-info.txt`, `xcaddy.txt`,
`extensions.txt`, `spc-cache.json` and `build-manifest.json`. Beside them
it writes what built the binary without the binary saying so: `spc.txt`,
the static-php-cli commit; `builder.txt`, the builder image by digest;
`craft.yml`, the file `build-static.sh` rendered for spc, which holds the
build's flags; and `recipe.txt`, the sha256 of the `Containerfile`
followed by `apps/<app>.env`. `cmd/frankenphp-manifest` reads them, from
the carrier's layers, into a manifest:

```sh
frankenphp-manifest -oci carrier.tar -arch arm64 -app librespeed
```

```json
{
  "schema": 3,
  "app": "librespeed",
  "arch": "arm64",
  "frankenphp": "v1.13.5",
  "php": "8.5.12",
  "caddy": "v2.11.9",
  "go": "go1.27.4",
  "xcaddy": "v0.4.7",
  "spc": "3ba9ffa0593b94202b48d6dde51e489191ecdbf1",
  "builder": "sha256:4dcdd53971ba1f373c3eb90e393dd3944687938c5c03494ab684921594905fc7",
  "craft": "4f6e7ebe03e0f498f5841813512d9fb01812d80fb0ee982839fc408cf09282a6",
  "recipe": "06b287fccf41e26f35a16f2daef3663306b271f7768aacb52cf15840fc60530f",
  "extensions": ["Core", "PDO", "Phar", "…"],
  "libraries": {
    "libaom": "dev-v3.15.3+b141d9c@b141d9c45db236335351c8a4b2a14b3ade27c937",
    "openssl": "openssl-3.6.5@89256e2bf2e5c761c9b4c49925d816f0e3b25fe4",
    "…": "…"
  },
  "go_modules": {"github.com/caddyserver/caddy/v2": "v2.11.9", "…": "…"}
}
```

`xcaddy` is the version Go recorded for the xcaddy that built the binary,
which `build/pinned-versions.sh` has already held to the Containerfile's
`XCADDY_VERSION`.

`spc`, `builder` and `craft` move with a change the binary does not
report: a new static-php-cli commit can change how every library is
patched and built, a new builder its compilers and headers, and the
rendered `craft.yml` the flags spc builds with. `recipe` moves with any
edit to the `Containerfile` or the app's file, a comment among them, so
any change to the pins, the build or the app's inputs moves the manifest,
while one to a check under `build/`, the workflows or the documentation
leaves the recipe as it was.
The build passes it as `RECIPE_SHA256`, which only the stage that writes
the records declares, so a new recipe never rebuilds the binary.

Each library is the version spc resolved, or the file it fetched when it
names none, then the hash of that source: a SHA-1 for an archive, the
commit for a git checkout. The manifest leaves out everything that differs
between two builds of the same inputs, such as the time spc fetched each
source, so two such builds give equal manifests even though their
carriers' digests differ. `frankenphp-manifest -compare A.json B.json`
says what moved between two.

The same command refuses a carrier whose binary carries a file capability,
or whose records are for another app or architecture.

## When a tag moves

- **Every merge to main** builds both architectures from scratch, and
  publishes them only if either architecture's manifest differs from the
  published one. A merge that changes only a check under `build/`, a
  workflow or the documentation changes nothing the binary is built
  from, so it publishes, and asks every app to move its digest, only if
  the Go toolchain or a library spc resolves at build time has moved since
  the published build.
- **Every day at 12:13 UTC** both are rebuilt from scratch, and published
  only if either architecture's manifest differs from the published one,
  whether main is still the published commit or has moved past it.
- **A manual run** of `build` on main publishes at once, whether or not
  anything moved.

The run's summary says what moved, or that nothing did. With nothing
published yet, a run publishes. A published carrier whose records this
commit cannot read, as after a change to what the manifest holds, counts
as moved: `published manifest unreadable (schema N)`, N being the newest
version of the manifest its records make. A merge's run on the commit
that is already published compares nothing and publishes nothing: it
could otherwise publish libraries its build fetched before the daily
run's over them.

Both architectures of a run publish together, or neither does, and a tag
never moves back to an older commit than the one it holds.

Each app publishes on its own. A build that fails for one app stops only
that app's publish, before it pushes anything, while the other app's goes
ahead; the run still fails, as a pull request with that failure does.

`:<app>` moves last. A publish pushes the new index as `:<app>-staging`,
attests it, verifies that attestation the way apps do, and checks that
moving the tag keeps the digest; only then does it point `:<app>` at that
digest. A publish that fails before then leaves `:<app>` on the index it
already held, though the staging and per-architecture tags may have moved.
Re-running the failed job publishes for a day after the build, while its
carriers are kept; after that, a manual run of `build` publishes at once,
and the next daily or push run does if its content moved. The exception
is a failure that names its own remedy: an index no anonymous
client can fetch points at the package's settings, and a dry run that
shows another digest says a re-run would meet the same.

## Working here

```sh
just setup                            # divbast/ci at the pinned commit, and the toolchain
GITHUB_TOKEN=… just build-app baikal  # localhost/frankenphp:baikal; the token is below
just ci                               # every gate; run it before committing
```

### A token for local builds

`just build-app` needs a GitHub token in `GITHUB_TOKEN`, and refuses to
build without one. The token is there only to lift the rate limit on spc's
calls to the GitHub API, which fail the build once the unauthenticated
limit runs out. It reaches the build as a secret, so no layer holds it, but
every build script spc runs while it builds the binary can read it: each
library's configure and make, and the Go build. The step that installs
xcaddy runs before that, without it.

So use a fine-grained personal access token with **Repository access:
Public repositories (read-only)** and no account or repository permissions
beyond the default metadata read. It reads only what anyone can read
anonymously, so a leak costs at most your API rate limit, and an expiry of
up to a year is reasonable: GitHub allows up to 366 days, or none, emails
before the token expires, and "Regenerate token" keeps its settings.

Keep it in a password manager or the OS keychain and pass it for one
command:

```sh
GITHUB_TOKEN="$(<your password manager's get command>)" just build-app librespeed
```

Don't export it from a shell profile or a `mise.toml` `[env]`: gh prefers
`GITHUB_TOKEN` over its own stored login (`gh help environment`), so your
own gh commands would start using the read-only token.

`just build-app` takes a token only when it starts with `github_pat_`, a
fine-grained personal access token, or `ghs_`, the job token Actions
issues, and refuses any other kind, gh's own `gho_` among them, naming the
kind it saw without printing the token. The check is divbast/ci's shared
`_github-token` recipe, which `build-app` runs first. The prefix shows the kind, not the
token's access. CI's build job passes its own read-only `ghs_` token to the
build directly, not through `just build-app`.

## Licence

BSD-2-Clause.

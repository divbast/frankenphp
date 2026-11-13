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

Each tag is a carrier, not a runnable image: built `FROM scratch`, it holds
the binary at `/frankenphp` and the records of how it was built under
`/records/`. The binary links glibc dynamically, so it runs on the app's own
base, such as `gcr.io/distroless/cc-debian13`, never on scratch. It carries
no file capability, so it runs under `--cap-drop ALL`.

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
- brotli, freetype, libavif, libjpeg, libpng, libwebp, libxml2, onig,
  openssl, watcher, xz and zlib: the newest GitHub release or tag that
  matches spc's rule for each;
- libiconv: the newest in GNU's file listing.

Of the libraries the two apps build, libaom comes from the tag above
instead, and sqlite and bzip2 from fixed URLs in spc's config. A rebuild
of an unchanged commit can therefore hold newer code in the others, which
is how their fixes arrive. The manifest below records each of these
inputs, so the daily rebuild publishes when one moves.

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
`extensions.txt`, `spc-cache.json` and `build-manifest.json`.
`cmd/frankenphp-manifest` reads them, from the carrier's layers, into a
manifest:

```sh
frankenphp-manifest -oci carrier.tar -arch arm64 -app librespeed
```

```json
{
  "schema": 2,
  "app": "librespeed",
  "arch": "arm64",
  "frankenphp": "v1.13.5",
  "php": "8.5.12",
  "caddy": "v2.11.9",
  "go": "go1.27.4",
  "xcaddy": "v0.4.7",
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

- **Every merge to main** builds both architectures from scratch and
  publishes them.
- **Every day at 12:13 UTC** both are rebuilt from scratch. When main has
  moved past the published commit, as after a merge whose own publish
  failed, the run publishes main. When main is still the published
  commit, it publishes only if either architecture's manifest differs from
  the published one, and the run's summary says what moved. Only this run
  compares manifests: a merge's run, or a re-run of an older one, could
  otherwise publish libraries fetched before the daily run's over them.
- **A manual run** of `build` on main publishes at once.

Both architectures of a run publish together, or neither does, and a tag
never moves back to an older commit than the one it holds.

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
kind it saw without printing the token. The prefix shows the kind, not the
token's access. CI's build job passes its own read-only `ghs_` token to the
build directly, not through `just build-app`.

## Licence

BSD-2-Clause.

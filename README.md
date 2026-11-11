<!-- SPDX-License-Identifier: BSD-2-Clause -->
<!-- SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee> -->

# frankenphp

The [FrankenPHP](https://github.com/php/frankenphp) binaries divbast's
images run: PHP 8.5 and Caddy in one binary, built once per app with that
app's PHP extensions, from pinned sources, and attested.

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

- the `dunglas/frankenphp:static-builder-gnu` image, by digest;
- PHP, Caddy and Caddy's three plugins, by version;
- static-php-cli (spc), which builds PHP and its C libraries, by commit.

`build/pinned-versions.sh` and `build/pinned-spc.sh` fail the build if the
binary disagrees with a pin, or if anything but the pinned spc ran.

What differs per app lives in `apps/<app>.env`: the PHP extensions and extra
libraries to build, and the exact list of modules the binary must load,
which `build/extensions.sh` checks. `cmd/frankenphp-buildargs` reads the file
and refuses anything else in it; `just build-app` and the build workflow
both build through it.

spc still chooses some sources while the build runs: openssl, zlib and
several other C libraries come from their newest release, and libaom from
its main branch. So a rebuild of an unchanged commit can hold newer C code,
and that is how library fixes arrive.

## The manifest

`build/records.sh` writes into the carrier what the binary reports about
itself and what spc recorded: `app.txt`, `version.txt`, `build-info.txt`,
`extensions.txt`, `spc-cache.json` and `build-manifest.json`.
`cmd/frankenphp-manifest` reads them, from the carrier's layers, into a
manifest:

```sh
frankenphp-manifest -oci carrier.tar -arch arm64 -app librespeed
```

```json
{
  "schema": 1,
  "app": "librespeed",
  "arch": "arm64",
  "frankenphp": "v1.13.5",
  "php": "8.5.12",
  "caddy": "v2.11.9",
  "go": "go1.27.4",
  "extensions": ["Core", "PDO", "Phar", "…"],
  "libraries": {
    "libaom": "dev-main+8a10385@8a1038536cde81c548341145f40d6dfddc3077d6",
    "openssl": "openssl-3.6.5@89256e2bf2e5c761c9b4c49925d816f0e3b25fe4",
    "…": "…"
  },
  "go_modules": {"github.com/caddyserver/caddy/v2": "v2.11.9", "…": "…"}
}
```

Each library is the version spc resolved, or the file it fetched when it
names none, then the hash of that source: a SHA-1 for an archive, the
commit for a git checkout. The manifest leaves out everything that differs
between two builds of the same inputs, such as the time spc fetched each
source, so two such builds give equal manifests even though their carriers'
digests differ. `frankenphp-manifest -compare A.json B.json` says what moved
between two.

The same command refuses a carrier whose binary carries a file capability,
or whose records are for another app or architecture.

## When a tag moves

- **Every merge to main** builds both architectures from scratch and
  publishes them.
- **Every day at 12:13 UTC** both are rebuilt from scratch, and published
  only if either architecture's manifest differs from the published one.
  The run's summary says what moved.
- **A manual run** of `build` on main publishes at once.

Both architectures of a run publish together, or neither does, and a tag
never moves back to an older commit than the one it holds. LibreSpeed's
binary takes libaom from its main branch, so its tag moves most days.

## Working here

```sh
just setup              # divbast/ci at the pinned commit, and the toolchain
just build-app baikal   # localhost/frankenphp:baikal
just ci                 # every gate; run it before committing
```

`just build-app` sends gh's token to spc as a build secret, so spc's
GitHub API calls escape the unauthenticated rate limit.

## Licence

BSD-2-Clause.

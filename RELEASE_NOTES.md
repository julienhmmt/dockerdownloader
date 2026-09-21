# dockerdownloader v0.0.1

First tagged release. `dockerdownloader` pulls a list of container images
**without a Docker daemon or any external binary**, retags them for a private
registry, and packages them into one integrity-checked `.tar.gz` / `.tar.zst`
bundle for airgapped infrastructure.

## Highlights

- **Daemonless and dependency-free.** Image I/O goes through
  [go-containerregistry](https://github.com/google/go-containerregistry); the
  tool is a single static binary with no runtime dependencies and no Docker
  client.
- **Verified end to end.** Every file in a bundle is hashed into
  `sha256sums.txt`. The generated `load.sh` verifies those checksums before
  loading and refuses to run at all if `sha256sums.txt` or a checksum tool is
  missing — closing the strip-the-manifest downgrade that would otherwise load
  unverified images.
- **The list is the trust boundary.** Every reference is validated before a byte
  is pulled, and two entries that would retag to the same destination are
  rejected up front rather than letting `docker load` keep only the last tag.
- **Batch resilience.** One failing image never aborts the run; failures are
  collected and reported while the rest continue, and results stay in input
  order.

## Features

- **Interactive TUI** (Bubble Tea): review and edit the image list, add images,
  purge cached tarballs, watch per-image download progress, and retry or skip
  individual failures.
- **Headless `batch` subcommand** for CI, plus `verify <bundle>` for an offline
  integrity check and `diff <a> <b>` for an image-level comparison of two
  bundles.
- **Retagging.** Each image is retagged to `<registry_prefix>/<path>:<tag>`
  inside the tarball, so `load.sh` pushes straight into your airgapped registry.
- **Resume.** With a fixed `work_dir`, `-resume` reuses tarballs from a previous
  run instead of re-pulling.
- **Reproducible bundles.** Set `SOURCE_DATE_EPOCH` and identical inputs produce
  byte-identical archives, so a bundle can be content-addressed or compared by
  hash across builds.
- **SPDX 2.3 SBOM** (`sbom.spdx.json`, one package per image) and a
  `manifest.json` recording tool version, platform, codec, and digests.
- **Preflight checks** for compression codec, theme, proxy URL, work dir, output
  dir, and free disk (`-min_free_mb`) — all before the long download path.
- **Digest pinning.** Resolved manifest digests land in `images.txt`,
  `manifest.json`, and a `.digest` sidecar used by `-resume`.

## Platforms

Prebuilt archives for `linux`, `macOS`, and `windows` on `amd64` and `arm64`,
with `checksums.txt` covering every artifact. To build from source you need
Go 1.27+.

## Install

```sh
go install github.com/julienhmmt/dockerdownloader@v0.0.1
```

Or download the archive for your platform below, unpack it, and run
`./dockerdownloader`.

## Known limitations

- One platform per run; multi-arch indexes are pulled for the configured
  `platform` only.
- Bundle tarballs stay tag-referenced — a docker-archive tar cannot be tagged by
  digest. Digests are recorded for verification, not for load identity.
- `load.sh` requires `sha256sum` or `shasum` on the airgapped host by design.

## License

AGPL-3.0.

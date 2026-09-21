# dockerdownloader

A single Go binary that pulls a list of container images and packages them into
one integrity-checked `.tar.gz` / `.tar.zst` bundle for airgapped
infrastructure. Interactive TUI, or headless for CI.

- **No Docker daemon, no external binaries.** Images are pulled with
  [go-containerregistry](https://github.com/google/go-containerregistry), so the
  tool is one static binary with zero runtime dependencies.
- **Retagged for your registry.** Every image is retagged to
  `<registry_prefix>/<original-path>:<tag>` inside the tarball, so the generated
  `load.sh` pushes straight into your airgapped registry.
- **Verified end to end.** Every file in the bundle is hashed into
  `sha256sums.txt`; `load.sh` refuses to load anything if the checksums do not
  match, and `dockerdownloader verify` re-checks a bundle offline.
- **Fail closed on a bad list.** Every reference is validated before a single
  byte is pulled, and two entries that would retag to the same destination are
  rejected rather than silently overwriting each other.

## Install

```sh
go install github.com/julienhmmt/dockerdownloader@latest
# or
make build          # → ./dockerdownloader
```

Requires Go 1.27+ to build. No runtime dependencies.

## Quick start

```sh
# 1. Settings
mkdir -p ~/.config/dockerdownloader
cp config.example.yaml ~/.config/dockerdownloader/config.yaml
$EDITOR ~/.config/dockerdownloader/config.yaml     # set registry_prefix

# 2. The image list
cp images.example.yaml images.yaml
$EDITOR images.yaml

# 3. Bundle
dockerdownloader                                   # TUI: review, then enter
# or, headless:
dockerdownloader batch images.yaml
```

Output: `archives/images-bundle.tar.gz`.

## Airgapped side

```sh
tar xzf images-bundle.tar.gz -C /tmp/bundle
cd /tmp/bundle && ./load.sh
```

`load.sh` verifies every checksum, loads each image, and pushes it to the
registry baked into its tag. It is idempotent (already-present images are
skipped), fails closed if `sha256sums.txt` or a checksum tool is missing, and
honours:

| Variable | Effect |
| -------- | ------ |
| `ENGINE=podman` | Use podman instead of docker |
| `DRY_RUN=1` | Print the commands without running them |

## The image list

```yaml
images:
  - nginx:1.27
  - quay.io/argoproj/argocd:v3.2.6
  - docker.io/library/redis:7.2
```

- Docker Hub shorthand is expanded (`redis:7` → `docker.io/library/redis:7`).
- Digest-pinned references are accepted; the destination is still a tag, because
  a docker-archive tarball cannot be tagged by digest.
- Entries are de-duplicated by canonical reference, and the list is validated
  before anything is pulled. An unreadable file, an unknown key, an unparseable
  reference, or an empty list is a hard error.
- A **missing** list file is tolerated by the TUI: it opens with an empty list
  (and a hint) so you can add images with `a` or purge the cache with `p`. The
  headless `batch` subcommand still requires the file.

## Bundle layout

```text
images-bundle.tar.gz
├── images/<sanitized-ref>.tar   one tarball per image, retagged
├── images.txt                   source_ref  dest_ref  tar_name  digest
├── manifest.json                tool, version, codec, platform, images + digests
├── sbom.spdx.json               SPDX 2.3, one package per image
├── sha256sums.txt               sha256 of every bundled file
└── load.sh                      verify, load, and push every image (0755)
```

## Commands

```text
dockerdownloader                                    # TUI
dockerdownloader -config cfg.yaml -images list.yaml # explicit paths
dockerdownloader batch [-config cfg.yaml] list.yaml # headless
dockerdownloader verify <bundle.tar.gz>             # integrity check, offline
dockerdownloader diff <a> <b>                       # image-level diff of two bundles
dockerdownloader version
```

### Flags

Every config field has a flag. `-config` and `-images` are the common ones;
`-registry-prefix`, `-platform`, `-name`, `-output`, `-work-dir`,
`-concurrency`, `-retries`, `-compression`, `-min-free-mb`, `-proxy`,
`-registry-auth`, `-resume`, `-theme`, `-v`, `-log-level`, `-log-file` all
override their config values. Run `dockerdownloader -h` for the full list.

## TUI keys

| Screen | Keys |
| ------ | ---- |
| Review | `space` toggle · `a` add image · `d` delete · `j`/`k` move · `pgup`/`pgdn` page · `g`/`G` jump · `p` purge cache · `enter` download · `esc` quit |
| Purge | `space` toggle · `a` all · `j`/`k` move · `pgup`/`pgdn` page · `g`/`G` jump · `enter` confirm · `esc` back |
| Download | `esc` cancel (keeps what already downloaded) |
| Failures | `r` retry failed · `c` continue with what downloaded · `q` abort |
| Any | `ctrl+t` theme menu · `ctrl+c` quit |

`p` lists the image tarballs cached under `work_dir/images/` so you can delete
individual ones and reclaim disk; a confirmation screen shows the count and the
space freed first. Deleting a cached image is safe — it is re-pulled on the next
run (with or without `-resume`). No persistent `work_dir` means nothing to purge,
so the key reports that and stays on Review.

## Configuration

`~/.config/dockerdownloader/config.yaml`, or `-config <path>`. Precedence:
CLI flags > config file > built-in defaults > environment (`HTTP_PROXY`, then
`HTTPS_PROXY`, for the proxy only). A missing config file is not an error;
defaults are used. Unknown keys are rejected, so a typo fails fast instead of
silently falling back to a default.

See [config.example.yaml](config.example.yaml) for every field with its default.
The ones that matter most:

| Field | Default | Notes |
| ----- | ------- | ----- |
| `registry_prefix` | `""` | Destination registry. Empty means no retagging. |
| `platform` | `linux/amd64` | One platform per run. |
| `images_file` | `images.yaml` | TUI input list. |
| `bundle_name` | `images` | Output file is `<name>-bundle.tar.gz`. |
| `compression` | `gzip` | `zstd` gives smaller bundles. |
| `min_free_disk_mb` | `2048` | Checked before the download starts. |
| `work_dir` | `""` | Fixed path enables `resume` and keeps tarballs between runs; purge cached tarballs in the TUI with `p`. |
| `resume` | `false` | Reuse tarballs from a prior run instead of re-pulling. |

## Development

```sh
make test        # go test ./...
make test-race   # required before calling a change done (the download path is concurrent)
make go-lint     # golangci-lint v2
make security    # vet + lint + govulncheck
```

See [AGENTS.md](AGENTS.md) for architecture and conventions.

## License

AGPL-3.0. See [LICENSE](LICENSE).

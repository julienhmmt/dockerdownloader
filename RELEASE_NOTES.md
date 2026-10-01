# dockerdownloader v0.0.2

Reliability fixes and small usability improvements for the daemonless container
image bundler. No new runtime dependencies.

## Fixes

- **Failure-safe bundle rebuilds.** Archives are written to a temporary file and
  published only after successful finalization. A failed rebuild preserves the
  previous bundle instead of truncating or deleting it.
- **Push the bundled image, not an older local tag.** Generated `load.sh` always
  loads the verified image archive before pushing. Re-running it is safe, and
  `DRY_RUN=1` still previews commands without executing the container engine.
- **Stricter offline verification.** `verify` rejects unsupported archive entries,
  including symbolic links, hard links, FIFOs, and device entries. Ordinary
  directories remain supported for repacked bundles.
- **Correct log-file precedence.** An explicit `-log-file` overrides the configured
  path; omitting the flag preserves the configured value.
- **Monotonic download progress.** Out-of-order completion notifications from
  concurrent downloads no longer move the TUI progress bar backwards.

## Improvements

- **Review select/deselect-all.** Press uppercase `A` to toggle all images;
  lowercase `a` still opens the add-image prompt.
- **Shared batch flags.** The headless `batch` subcommand accepts the same download
  and logging flags as the TUI, with the same configuration precedence. Put flags
  before the positional image list:

  ```sh
  dockerdownloader batch -output archives -work-dir ./cache -resume -platform linux/arm64 images.yaml
  ```

  Run `dockerdownloader batch -h` for the full list. `-images` and `-theme` remain
  TUI-only.

## Platforms

Prebuilt archives for Linux, macOS, and Windows on amd64 and arm64, with
`checksums.txt` covering every artifact. Building from source requires Go 1.27+.

## Install

```sh
go install github.com/julienhmmt/dockerdownloader@v0.0.2
```

Or download the archive for your platform below, unpack it, and run
`./dockerdownloader`.

## Compatibility

- Changes to generated `load.sh` apply to newly created bundles. Existing bundles
  retain their original script.
- One platform per run. Bundle images remain tag-referenced, with digests recorded
  for verification rather than load identity.
- `load.sh` requires Docker or Podman and `sha256sum` or `shasum` on the airgapped
  host. The downloader itself remains daemonless and dependency-free.

## License

AGPL-3.0.

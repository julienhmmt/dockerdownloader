# dockerdownloader v0.0.3

Maintenance release: security toolchain bump, reliability fixes, and dependency
updates. No new features and no new runtime dependencies.

## Security

- **Go 1.27.3 required.** Earlier 1.27.x releases ship the crypto/x509 flaw
  GO-2026-6809, reachable through TLS registry pulls. Prebuilt binaries are
  built with 1.27.3.
- **Reject malformed resume digests.** `.digest` sidecars that are not
  `sha256:<64 hex>` are refused, closing a `load.sh` injection path through a
  tampered work dir.

## Fixes

- **Batch exit codes.** `batch` always removes its temporary work dir and exits
  `3` when a bundle was written but some images failed. An interrupted download
  writes no partial bundle; Ctrl-C while bundling cleans up and exits `130`.
  Exit codes are documented in the README.
- **Safe re-pulls.** Tarballs are written to a temp file and renamed on success,
  so a failed re-pull never truncates a cached tarball. A pull whose digest
  cannot be resolved now fails instead of producing an unpinned image.
- **No pointless retries.** Permanent registry errors (401, 403, 404, ...) fail
  immediately; 408, 429, 5xx and network errors are still retried.
- **Full-disk preflight.** A disk with 0 bytes free now fails the free-space
  check instead of being treated as unknown.
- **TUI retry handling.** A cancelled retry reports its images as failures
  instead of dropping them, `r` with no failures is a no-op, and a new pass
  waits for a cancelled one to stop writing.

## Dependencies

bubbletea v2.1.1, klauspost/compress v1.20.3, golang.org/x/sync v0.24.0,
golang.org/x/sys v0.49.0, and transitive updates. `govulncheck` reports no
vulnerabilities.

## Install

```sh
go install github.com/julienhmmt/dockerdownloader@v0.0.3
```

Or download the archive for your platform below (Linux, macOS, Windows on
amd64 and arm64; `checksums.txt` covers every artifact).

## Compatibility

Bundles produced by v0.0.2 remain valid. Building from source requires
Go 1.27.3+.

## License

AGPL-3.0.

# AGENTS.md

Agent guidance for [dockerdownloader](https://github.com/julienhmmt/dockerdownloader).

Keep this file short; it is the whole guide. If it starts to sprawl, split depth
into `.agents/skills/`.

## What this is

Terminal UI (Bubble Tea) plus a headless `batch` subcommand that pulls a list of
container images **daemonlessly** (no Docker), retags them for a private
registry, and bundles them into a single integrity-checked `.tar.gz` /
`.tar.zst` for airgapped infrastructure. Generated `load.sh` loads and pushes
every image on the airgapped side.

Also: `dockerdownloader verify <bundle>` and `dockerdownloader diff <a> <b>`.

Images only, with **no external binary dependency at all**.

Module: `github.com/julienhmmt/dockerdownloader` · Go **1.26+** · License **AGPL-3.0**

## Commands (Makefile)

| Target | Command | Notes |
| ------ | ------- | ----- |
| Build | `make build` | `go build -o dockerdownloader .` |
| Release build | `make build-release` | stripped + trimpath |
| Test | `make test` | `go test ./... -count=1` |
| Test + race | `make test-race` | **required** before done (pipeline is concurrent) |
| Lint | `make go-lint` | golangci-lint v2 |
| Vet | `make go-vet` | |
| Vulns | `make govulncheck` | |
| Security suite | `make security` | vet + lint + vuln |
| Install | `make install` | |

Single package: `go test ./pkg/pipeline/ -run TestName -v`

## Architecture

```text
Load (images.yaml) → Review (TUI) → Download (parallel registry.Save)
  → Bundle (.tar.gz | .tar.zst) → Done
```

Entry: `main.go` loads `config`, merges CLI flags, validates compression/theme,
resolves the work dir, then loads + validates the image list (fail closed) and
hands it to `tui.Run`. The TUI owns screens; `pkg/pipeline` owns orchestration.

| Package | Responsibility | Key APIs |
| ------- | -------------- | -------- |
| `pkg/config` | YAML + defaults | `Config`, `Default()`, `Load`, `LoadRequired` |
| `pkg/imagelist` | The input list and all reference arithmetic | `Load`, `Parse`, `ValidRef`, `PullRef`, `Retag`, `ValidateDest` |
| `pkg/registry` | Daemonless pull | `Puller.Save(ctx, src, dest, path, onBytes) (digest, err)` |
| `pkg/bundle` | Archive + verify | `Create(Spec)`, `Verify`, `Diff`; codecs in `compress.go` |
| `pkg/pipeline` | Orchestration | `NewSession`, `Download`, `Bundle`; seam `imageSaver` |
| `pkg/log` | File logger | silent / info / debug |
| `internal/tui` | Bubble Tea UI | model / update / view / commands split |

## Design invariants (do not break)

1. **Daemonless, dependency-free**: no Docker client/daemon, no external binary.
   Image I/O is go-containerregistry only.
2. **The list is the trust boundary**: every ref is validated in
   `imagelist.Parse` before anything is pulled, and `imagelist.ValidateDest`
   rejects two entries that would retag to the same destination — otherwise
   `docker load` would keep only the last tag and silently drop an image — and
   rejects a destination that is not a valid tag ref (a malformed
   `registry_prefix`) at startup rather than per-image after a full download.
3. **Digests are pinned**: `registry.Save` returns the resolved manifest digest →
   `images.txt`, `manifest.json`, and `.digest` sidecar (for `-resume`).
   Tarballs stay **tag-referenced** (a docker tar cannot be digest-tagged);
   digest is for verification, not load identity.
4. **Batch resilience**: `Download` tries every image; returns
   `[]bundle.ImageEntry` + `[]ImageFailure`. Never abort the batch on one
   failure. Fixed-slot result array keeps **input order**.
5. **Retry**: `saveWithRetry` exponential backoff; cancellable via context.
   Tests shrink `retryBaseDelay`.
6. **Preflight**: compression codec, theme, work dir, output dir, free disk
   (`-min-free-mb`) fail before the long download path.
7. **Bundle integrity**: every file hashed into `sha256sums.txt`, and `Verify`
   rejects both a mismatch and an archive entry the manifest does not list.
   Generated `load.sh` verifies checksums, refuses to run when
   `sha256sums.txt` or a checksum tool is missing, is idempotent, honors
   `DRY_RUN=1` and `ENGINE`.
8. **A bundle always has images**: `bundle.Create` rejects an empty image set,
   and the TUI refuses to start a download with nothing selected.
9. **Resume**: with a fixed `-work-dir` + `-resume`, reuse existing tarballs and
   `.digest` sidecars instead of re-pulling. `Bundle` deliberately does **not**
   delete work-dir tarballs, so `-resume` can rebuild without re-pulling.

## Bundle layout

```text
<name>-bundle.tar.{gz|zst}
├── images/<sanitized-ref>.tar
├── images.txt              # source_ref  dest_ref  tar_name  digest
├── manifest.json           # provenance: tool, name, platform, codec, digests
├── sbom.spdx.json          # SPDX 2.3: one package per image
├── sha256sums.txt
└── load.sh
```

## Where to put new code

| Change | Location |
| ------ | -------- |
| New CLI flag / config field | `pkg/config` + `main.go` + README + `config.example.yaml` |
| List parsing, ref validation, retagging | `pkg/imagelist` (unit tests first) |
| Pull / save / auth / progress | `pkg/registry` |
| Archive contents / load.sh / verify | `pkg/bundle` |
| Concurrency / retry / resume / disk | `pkg/pipeline` |
| New TUI screen or keybinding | `internal/tui` (messages → update → view) |
| Platform-specific syscall | build-tagged files next to caller |

## Hard rules

1. **Always run `make test-race`** before considering a change done.
2. **Preserve the test seam**: `imageSaver` in `pkg/pipeline` — tests inject a
   fake; do not couple production types into test-only paths.
3. **Config is one source of truth**: new setting = field on `config.Config` +
   default in `config.Default` + CLI flag in `main.go` + README + example.
   `config.Load` rejects unknown YAML keys, so keep `config.example.yaml` in
   sync with the struct.
4. **Wrap errors**: `fmt.Errorf("...: %w", err)`. `errorlint` enforces this.
5. **Do not add a Docker/daemon dependency**, and do not add a new external
   binary requirement — the zero-dependency story is the point.
6. **Platform code**: use build tags (`diskspace_unix.go` / `diskspace_other.go`);
   always keep a no-op fallback so non-unix builds compile.
7. **gofmt / goimports** mandatory; local-prefix is
   `github.com/julienhmmt/dockerdownloader`.
8. **English** for code and docs. Prefer small, single-purpose files (see
   `internal/tui` model/update/view split).

## Non-goals / do not

- Do not invent a new task runner (Makefile is canonical).
- Do not commit secrets, `.env`, logs, binaries, or `archives/`.
- Do not change CI security steps or weaken lint just to pass green.
- Do not break batch download behavior: one image failure must not abort the
  rest (`[]ImageFailure`); results stay in **input order** (fixed-slot array).
- Do not make `Bundle` delete work-dir tarballs; that would break `-resume`.

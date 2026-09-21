// Package pipeline orchestrates the end-to-end flow: pulling a list of
// container images and assembling them into a single airgap bundle.
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/julienhmmt/dockerdownloader/pkg/bundle"
	"github.com/julienhmmt/dockerdownloader/pkg/config"
	"github.com/julienhmmt/dockerdownloader/pkg/imagelist"
	"github.com/julienhmmt/dockerdownloader/pkg/log"
	"github.com/julienhmmt/dockerdownloader/pkg/registry"
)

// Session is the working directory for one bundling run.
type Session struct {
	// WorkDir holds intermediate artifacts (the image tarballs).
	WorkDir string
	// TempWorkDir reports whether WorkDir was created as a temporary directory
	// by NewSession and should be removed on cleanup; it is false when WorkDir
	// is the user-configured cfg.WorkDir, which is preserved across runs for
	// -resume.
	TempWorkDir bool
}

// imageSaver pulls a source image and writes it to a tarball retagged as
// destRef, returning the resolved manifest digest. onBytes, when non-nil,
// receives byte-level progress during the write. *registry.Puller is the
// production implementation; tests substitute a fake.
type imageSaver interface {
	Save(ctx context.Context, srcRef, destRef, destPath string, onBytes registry.BytesFunc) (string, error)
}

// defaultRetryBaseDelay is the first backoff interval; it doubles each retry.
const defaultRetryBaseDelay = 1 * time.Second

// Pipeline downloads and bundles images using the configured image puller.
type Pipeline struct {
	cfg    config.Config
	puller imageSaver
	logger *log.Logger
	// retryBaseDelay is the first backoff interval between pull attempts.
	// Tests shrink it to keep retry coverage fast.
	retryBaseDelay time.Duration
}

// New returns a Pipeline configured from cfg.
func New(cfg config.Config, logger *log.Logger) *Pipeline {
	return &Pipeline{
		cfg:            cfg,
		puller:         registry.NewPuller(cfg.Platform, cfg.HTTPSProxy, cfg.RegistryAuth, logger),
		logger:         logger,
		retryBaseDelay: defaultRetryBaseDelay,
	}
}

// NewSession creates the working directory for a run: cfg.WorkDir when set
// (preserved across runs for -resume), otherwise a fresh temporary directory.
func (p *Pipeline) NewSession() (Session, error) {
	if p.cfg.WorkDir != "" {
		if err := os.MkdirAll(p.cfg.WorkDir, 0o755); err != nil {
			return Session{}, fmt.Errorf("create work dir: %w", err)
		}
		p.logger.Debugf("work dir: %s", p.cfg.WorkDir)
		return Session{WorkDir: p.cfg.WorkDir}, nil
	}
	dir, err := os.MkdirTemp(p.cfg.TempDir, "dockerdownloader-")
	if err != nil {
		return Session{}, fmt.Errorf("create temp work dir: %w", err)
	}
	p.logger.Debugf("work dir: %s", dir)
	return Session{WorkDir: dir, TempWorkDir: true}, nil
}

// ProgressFunc reports download progress as each image is processed.
type ProgressFunc func(current, total int, ref string, err error)

// ByteProgressFunc reports byte-level progress for an in-flight image pull.
// total is best-effort and may be 0 when the registry does not report sizes.
type ByteProgressFunc func(ref string, written, total int64)

// ImageFailure records an image reference that could not be downloaded and the
// error that prevented it.
type ImageFailure struct {
	Ref string
	Err error
}

// Download saves the given image references into the session's work directory,
// returning the successful bundle entries and any failures. Images are pulled
// in parallel up to the configured concurrency limit. It does not assemble the
// bundle, so callers can present failures to the user and retry the failed
// references before committing to a bundle.
//
// The returned entries and failures preserve the order of refs. Progress is
// reported as each image finishes; current is the number completed so far,
// which may not match the position of ref in refs because pulls finish out of
// order.
func (p *Pipeline) Download(ctx context.Context, session Session, refs []string, progress ProgressFunc, byteProgress ByteProgressFunc) ([]bundle.ImageEntry, []ImageFailure, error) {
	limit := p.concurrency()
	p.logger.Infof("downloading %d images (concurrency %d)", len(refs), limit)
	imagesDir := filepath.Join(session.WorkDir, "images")
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create images dir: %w", err)
	}
	if err := p.checkDiskSpace(imagesDir); err != nil {
		return nil, nil, err
	}

	// Each ref writes its outcome to a fixed slot so the final results stay in
	// input order regardless of completion order.
	type result struct {
		entry *bundle.ImageEntry
		fail  *ImageFailure
	}
	results := make([]result, len(refs))

	var (
		mu        sync.Mutex
		completed int
	)
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(limit)
	for index, ref := range refs {
		group.Go(func() error {
			srcRef := imagelist.PullRef(ref)
			destRef := imagelist.Retag(ref, p.cfg.RegistryPrefix)
			tarPath := filepath.Join(imagesDir, tarballName(ref))

			// Resume: reuse a tarball already on disk from a prior run instead of
			// pulling it again. The digest sidecar restores the manifest digest so
			// the bundle stays fully pinned; the meta sidecar gates reuse on the
			// destination ref and platform, so a settings change re-pulls rather
			// than bundling a stale tarball under new provenance.
			wantMeta := resumeMeta{SourceRef: ref, DestRef: destRef, Platform: p.cfg.Platform}
			if p.cfg.Resume {
				if digest, ok := p.reusableTarball(tarPath, wantMeta); ok {
					p.logger.Infof("reusing existing tarball for %s", ref)
					mu.Lock()
					completed++
					done := completed
					results[index] = result{entry: &bundle.ImageEntry{
						TarPath: tarPath, SourceRef: ref, DestRef: destRef, Digest: digest,
					}}
					mu.Unlock()
					if progress != nil {
						progress(done, len(refs), ref, nil)
					}
					return nil
				}
			}

			p.logger.Debugf("saving image %d/%d: %s -> %s", index+1, len(refs), srcRef, destRef)
			var onBytes registry.BytesFunc
			if byteProgress != nil {
				onBytes = func(written, total int64) { byteProgress(ref, written, total) }
			}
			digest, err := p.saveWithRetry(groupCtx, srcRef, destRef, tarPath, onBytes)

			mu.Lock()
			completed++
			done := completed
			if err != nil {
				results[index] = result{fail: &ImageFailure{Ref: ref, Err: err}}
			} else {
				results[index] = result{entry: &bundle.ImageEntry{
					TarPath:   tarPath,
					SourceRef: ref,
					DestRef:   destRef,
					Digest:    digest,
				}}
				// Record registry digest + content hash beside the tarball so a
				// later -resume run can reuse it without re-pulling and still
				// pin the bundle with verified file bytes.
				writeResumeSidecars(tarPath, digest, wantMeta)
			}
			mu.Unlock()

			if err != nil {
				p.logger.Errorf("failed to save %s: %v", ref, err)
			}
			if progress != nil {
				progress(done, len(refs), ref, err)
			}
			// A failed pull is recorded, not propagated: we want every image
			// attempted so the user sees the full set of failures at once.
			return nil
		})
	}
	_ = group.Wait()

	entries := make([]bundle.ImageEntry, 0, len(refs))
	failures := make([]ImageFailure, 0)
	for _, r := range results {
		switch {
		case r.entry != nil:
			entries = append(entries, *r.entry)
		case r.fail != nil:
			failures = append(failures, *r.fail)
		}
	}
	return entries, failures, nil
}

// Bundle assembles the downloaded image entries into a single archive and
// returns its path. Intermediate tarballs are left in place: a persistent
// work_dir keeps them so -resume can rebuild the bundle without re-pulling,
// and a temporary one is removed by the caller on exit.
func (p *Pipeline) Bundle(entries []bundle.ImageEntry) (string, error) {
	p.logger.Infof("creating bundle %q with %d images", p.cfg.BundleName, len(entries))
	bundlePath, err := bundle.Create(bundle.Spec{
		Name:        p.cfg.BundleName,
		Platform:    p.cfg.Platform,
		Images:      entries,
		OutputDir:   p.cfg.OutputDir,
		Compression: p.cfg.Compression,
	})
	if err != nil {
		return "", err
	}
	p.logger.Infof("bundle created: %s", bundlePath)
	return bundlePath, nil
}

// checkDiskSpace fails fast when the filesystem backing dir has less free space
// than the configured minimum, so a long download does not abort mid-way with a
// cryptic "no space left" error. A 0 threshold, an unsupported platform, or a
// stat error all skip the check rather than block progress.
func (p *Pipeline) checkDiskSpace(dir string) error {
	if p.cfg.MinFreeDiskMB <= 0 {
		return nil
	}
	free, err := freeBytes(dir)
	if err != nil {
		p.logger.Debugf("disk space check skipped: %v", err)
		return nil
	}
	if free == 0 {
		return nil
	}
	const mib = 1 << 20
	needed := uint64(p.cfg.MinFreeDiskMB) * mib
	if free < needed {
		return fmt.Errorf("insufficient disk space in %s: %d MiB free, need at least %d MiB",
			dir, free/mib, p.cfg.MinFreeDiskMB)
	}
	p.logger.Debugf("disk space ok: %d MiB free in %s", free/mib, dir)
	return nil
}

// concurrency returns the effective parallel download limit, never below 1.
func (p *Pipeline) concurrency() int {
	if p.cfg.Concurrency < 1 {
		return 1
	}
	return p.cfg.Concurrency
}

// retries returns the number of additional pull attempts, never below 0.
func (p *Pipeline) retries() int {
	if p.cfg.Retries < 0 {
		return 0
	}
	return p.cfg.Retries
}

// saveWithRetry pulls srcRef, retrying transient failures with exponential
// backoff up to the configured retry count, and returns the resolved digest on
// success. Backoff waits are cancellable: if ctx is done, the last error is
// returned immediately without sleeping.
func (p *Pipeline) saveWithRetry(ctx context.Context, srcRef, destRef, tarPath string, onBytes registry.BytesFunc) (string, error) {
	attempts := p.retries() + 1
	delay := p.retryBaseDelay
	if delay <= 0 {
		delay = defaultRetryBaseDelay
	}
	var (
		digest string
		err    error
	)
	for attempt := 1; attempt <= attempts; attempt++ {
		if digest, err = p.puller.Save(ctx, srcRef, destRef, tarPath, onBytes); err == nil {
			return digest, nil
		}
		if ctx.Err() != nil || attempt == attempts {
			break
		}
		p.logger.Debugf("retry %d/%d for %s after %s: %v", attempt, attempts-1, srcRef, delay, err)
		select {
		case <-ctx.Done():
			return "", err
		case <-time.After(delay):
		}
		delay *= 2
	}
	return "", err
}

// digestSidecarPath returns the path of the file recording a tarball's
// registry manifest digest.
func digestSidecarPath(tarPath string) string {
	return tarPath + ".digest"
}

// contentHashSidecarPath returns the path of the file recording the sha256 of
// the tarball bytes (resume integrity gate).
func contentHashSidecarPath(tarPath string) string {
	return tarPath + ".sha256"
}

// metaSidecarPath returns the path of the file recording the settings that
// shaped a tarball (resume provenance gate).
func metaSidecarPath(tarPath string) string {
	return tarPath + ".meta"
}

// resumeMeta records the settings that shaped a cached tarball. -resume only
// reuses a tarball whose recorded destination ref and platform match the
// current run, so changing registry_prefix or platform re-pulls instead of
// bundling an image whose embedded RepoTag/architecture no longer matches the
// bundle's provenance.
type resumeMeta struct {
	SourceRef string `json:"sourceRef"`
	DestRef   string `json:"destRef"`
	Platform  string `json:"platform"`
}

// fileSHA256 streams path and returns its hex-encoded sha256.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeResumeSidecars records the registry manifest digest, the settings that
// shaped the tarball, and a content hash for later -resume runs. Failure is
// non-fatal: the tarball is still valid; resume just will not reuse it without
// every sidecar present and matching.
func writeResumeSidecars(tarPath, registryDigest string, meta resumeMeta) {
	if registryDigest != "" {
		_ = os.WriteFile(digestSidecarPath(tarPath), []byte(registryDigest), 0o600)
	}
	if data, err := json.Marshal(meta); err == nil {
		_ = os.WriteFile(metaSidecarPath(tarPath), data, 0o600)
	}
	sum, err := fileSHA256(tarPath)
	if err != nil {
		return
	}
	_ = os.WriteFile(contentHashSidecarPath(tarPath), []byte(sum), 0o600)
}

// readResumeMeta reads and parses the meta sidecar at tarPath. It reports false
// when the sidecar is missing, malformed, or lacks a source/dest ref — a
// tarball from an older binary has no meta and must be treated as not reusable,
// or the first run after upgrading would reintroduce the stale-bundle bug.
func readResumeMeta(tarPath string) (resumeMeta, bool) {
	data, err := os.ReadFile(metaSidecarPath(tarPath))
	if err != nil {
		return resumeMeta{}, false
	}
	var meta resumeMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return resumeMeta{}, false
	}
	if meta.SourceRef == "" || meta.DestRef == "" {
		return resumeMeta{}, false
	}
	return meta, true
}

// reusableTarball reports whether a complete tarball already exists at tarPath
// with a recorded registry digest, matching content-hash sidecar, and a meta
// sidecar matching want, returning that registry digest. Reuse requires
// non-empty file, non-empty .digest, matching .sha256 of file bytes, a meta
// matching the current destination ref and platform, and tarballComplete — so a
// tampered, truncated, or settings-mismatched tar is re-pulled. A rejection is
// logged at debug level under -v.
func (p *Pipeline) reusableTarball(tarPath string, want resumeMeta) (string, bool) {
	info, err := os.Stat(tarPath)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return "", false
	}
	meta, ok := readResumeMeta(tarPath)
	if !ok {
		p.logger.Debugf("resume: %s has no matching metadata sidecar, re-pulling", tarPath)
		return "", false
	}
	if meta != want {
		p.logger.Debugf("resume: %s was built for %s on %s, re-pulling for %s on %s",
			tarPath, meta.DestRef, meta.Platform, want.DestRef, want.Platform)
		return "", false
	}
	data, err := os.ReadFile(digestSidecarPath(tarPath))
	if err != nil {
		p.logger.Debugf("resume: %s has no digest sidecar, re-pulling", tarPath)
		return "", false
	}
	digest := strings.TrimSpace(string(data))
	if digest == "" {
		p.logger.Debugf("resume: %s has empty digest sidecar, re-pulling", tarPath)
		return "", false
	}
	wantContent, err := os.ReadFile(contentHashSidecarPath(tarPath))
	if err != nil || strings.TrimSpace(string(wantContent)) == "" {
		p.logger.Debugf("resume: %s missing content hash sidecar, re-pulling", tarPath)
		return "", false
	}
	got, err := fileSHA256(tarPath)
	if err != nil || got != strings.TrimSpace(string(wantContent)) {
		p.logger.Debugf("resume: %s content hash mismatch, re-pulling", tarPath)
		return "", false
	}
	if !tarballComplete(tarPath) {
		p.logger.Debugf("resume: %s failed integrity check, re-pulling", tarPath)
		return "", false
	}
	return digest, true
}

// tarballComplete reports whether tarPath looks like a complete tar archive by
// checking the two trailing 512-byte zero blocks that terminate a well-formed
// tar. It is a cheap integrity gate for -resume: a truncated file fails here
// and is re-pulled rather than silently bundled. It is deliberately pure (no
// logger) so it stays unit-testable.
func tarballComplete(tarPath string) bool {
	f, err := os.Open(tarPath)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil || stat.Size() < 1024 || stat.Size()%512 != 0 {
		return false
	}
	buf := make([]byte, 1024)
	if _, err := f.ReadAt(buf, stat.Size()-1024); err != nil {
		return false
	}
	for _, b := range buf {
		if b != 0 {
			return false
		}
	}
	return true
}

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

// tarballName derives a filesystem-safe tar filename from an image reference.
// A short hash of the original ref is appended so two distinct refs that
// sanitize to the same string (e.g. "foo/bar:1" and "foo_bar.1") cannot map to
// the same filename — which would let concurrent pulls O_TRUNC each other's
// tarball and silently corrupt the bundle.
func tarballName(ref string) string {
	safe := unsafeChars.ReplaceAllString(ref, "_")
	safe = strings.Trim(safe, "_")
	sum := sha256.Sum256([]byte(ref))
	return fmt.Sprintf("%s-%x.tar", safe, sum[:4])
}

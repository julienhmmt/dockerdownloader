// Package registry pulls container images directly from registries (no Docker
// daemon) and writes them to docker-compatible tar archives, retagged for the
// destination private registry.
package registry

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/tarball"

	"github.com/julienhmmt/dockerdownloader/pkg/log"
)

// BytesFunc reports cumulative bytes written for an image and the best-effort
// total. total may be 0 when it cannot be determined up front.
type BytesFunc func(written, total int64)

// progressThreshold is the minimum number of new bytes between BytesFunc calls,
// so a fast stream does not flood the caller with updates.
const progressThreshold = 512 * 1024

// countingWriter forwards writes while reporting cumulative progress no more
// often than progressThreshold bytes.
type countingWriter struct {
	w        io.Writer
	total    int64
	written  int64
	reported int64
	onBytes  BytesFunc
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.written += int64(n)
	if c.onBytes != nil && (c.written-c.reported >= progressThreshold) {
		c.reported = c.written
		c.onBytes(c.written, c.total)
	}
	return n, err
}

// Puller fetches and saves images for a fixed target platform.
type Puller struct {
	platform string
	proxy    string
	auth     bool
	logger   *log.Logger
	// transport is the proxied http.Transport built once and reused across
	// every Save call, so the batch shares one warm TLS connection pool instead
	// of handshaking per image. It is nil when no proxy is configured (crane's
	// shared default transport is used in that case). initOnce guards the
	// lazy build so the proxy URL is parsed at most once and the error surfaces
	// from buildOpts with the same message as before.
	transport    *http.Transport
	transportErr error
	initOnce     sync.Once
}

// NewPuller returns a Puller that pulls images for the given platform string,
// e.g. "linux/amd64". When auth is true, pulls authenticate via the default
// Docker keychain (DOCKER_CONFIG or ~/.docker/config.json) so private
// registries can be reached.
func NewPuller(platform, proxy string, auth bool, logger *log.Logger) *Puller {
	if platform == "" {
		platform = "linux/amd64"
	}
	return &Puller{platform: platform, proxy: proxy, auth: auth, logger: logger}
}

// buildOpts assembles the crane options for a pull: platform, proxy (when
// configured), auth (when enabled), and the context. Extracted for
// testability so the auth/proxy wiring can be asserted without a network
// pull.
func (p *Puller) buildOpts(ctx context.Context) ([]crane.Option, error) {
	platform, err := v1.ParsePlatform(p.platform)
	if err != nil {
		return nil, fmt.Errorf("parse platform %q: %w", p.platform, err)
	}
	opts := []crane.Option{crane.WithPlatform(platform)}
	if p.proxy != "" {
		transport, err := p.proxiedTransport()
		if err != nil {
			return nil, err
		}
		opts = append(opts, crane.WithTransport(transport))
	}
	if p.auth {
		opts = append(opts, crane.WithAuthFromKeychain(authn.DefaultKeychain))
	}
	opts = append(opts, crane.WithContext(ctx))
	return opts, nil
}

// proxiedTransport returns the cached proxied http.Transport, building it on
// first call. It clones the stdlib default transport so we keep its sane
// timeouts, idle connection pool, and HTTP/2 support, overriding only the
// proxy. The transport is shared across every Save call so the concurrent
// batch reuses one warm TLS pool instead of handshaking per image.
func (p *Puller) proxiedTransport() (*http.Transport, error) {
	p.initOnce.Do(func() {
		proxyURL, err := url.Parse(p.proxy)
		if err != nil {
			p.transportErr = fmt.Errorf("parse proxy URL %q: %w", p.proxy, err)
			return
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyURL(proxyURL)
		p.transport = transport
	})
	return p.transport, p.transportErr
}

// transportForTest exposes the cached proxied transport for test assertions.
// It is nil when no proxy is configured or before buildOpts has run.
func (p *Puller) transportForTest() *http.Transport { return p.transport }

// Save pulls srcRef for the configured platform and writes it to destPath as a
// docker-style tarball, embedding destRef as the image's tag so a later
// "docker load" yields the retagged image ready to push to the airgap registry.
//
// It returns the resolved manifest digest of the pulled, platform-specific
// image (e.g. "sha256:..."). The digest pins exactly what was bundled so the
// airgapped side can verify it, even though the tarball itself is tagged rather
// than digest-referenced.
func (p *Puller) Save(ctx context.Context, srcRef, destRef, destPath string, onBytes BytesFunc) (string, error) {
	p.logger.Infof("pulling image %s for platform %s", srcRef, p.platform)
	opts, err := p.buildOpts(ctx)
	if err != nil {
		return "", err
	}
	// Pull the image. The timeout configuration is not used here because
	// crane.Pull returns a lazy-loading image that retains the context,
	// which would cause the layer write below to time out during downloads.
	img, err := crane.Pull(srcRef, opts...)
	if err != nil {
		return "", fmt.Errorf("pull %s: %w", srcRef, err)
	}

	tag, err := name.NewTag(destRef)
	if err != nil {
		return "", fmt.Errorf("parse dest ref %q: %w", destRef, err)
	}

	// Resolve the digest before writing: it pins exactly what is bundled, so an
	// image whose digest cannot be resolved is a failed pull, not a success with
	// an empty digest that verify would later reject.
	digest, err := img.Digest()
	if err != nil {
		return "", fmt.Errorf("resolve digest for %s: %w", srcRef, err)
	}

	p.logger.Debugf("saving %s to %s", destRef, destPath)
	if err := writeTarballAtomic(destPath, func(w io.Writer) error {
		cw := &countingWriter{w: w, total: estimateSize(img, p.logger), onBytes: onBytes}
		return tarball.Write(tag, img, cw)
	}); err != nil {
		return "", fmt.Errorf("save %s: %w", destRef, err)
	}

	p.logger.Infof("saved %s (%s)", destPath, digest)
	return digest.String(), nil
}

// writeTarballAtomic streams write into a temporary file beside destPath and
// renames it into place only on success. A failed or cancelled pull therefore
// never truncates or removes a tarball already at destPath (a -resume cache
// entry, or a concurrent pass's output). The temp name does not end in .tar so
// the cache listing never shows a half-written file.
func writeTarballAtomic(destPath string, write func(io.Writer) error) error {
	file, err := os.CreateTemp(filepath.Dir(destPath), "."+filepath.Base(destPath)+".*.tmp")
	if err != nil {
		return fmt.Errorf("cannot create file in destination directory: %w", err)
	}
	tmpPath := file.Name()
	if err := write(file); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	// CreateTemp uses 0600; keep the 0644 the tarball always had.
	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod %s: %w", tmpPath, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename %s: %w", tmpPath, err)
	}
	return nil
}

// estimateSize sums an image's compressed layer sizes and config size as a
// best-effort total for progress reporting. It returns 0 when sizes cannot be
// resolved (e.g. a registry that omits them), in which case progress is
// reported as raw bytes with no percentage.
func estimateSize(img v1.Image, logger *log.Logger) int64 {
	var total int64
	layers, err := img.Layers()
	if err != nil {
		logger.Debugf("could not list layers for size estimate: %v", err)
		return 0
	}
	for _, l := range layers {
		s, err := l.Size()
		if err != nil {
			return 0
		}
		total += s
	}
	if m, err := img.Manifest(); err == nil {
		total += m.Config.Size
	}
	return total
}

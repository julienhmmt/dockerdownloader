// Package imagelist parses and validates the YAML list of container images to
// bundle, and owns the reference arithmetic (canonical pull ref, retag for the
// destination registry) used across the pipeline.
package imagelist

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"gopkg.in/yaml.v3"
)

// Image is a single container image reference from the input list.
type Image struct {
	// Ref is the original reference as written by the user, e.g. "nginx:1.27".
	Ref string
	// Selected indicates whether the user wants it included in the bundle.
	Selected bool
}

// listFile is the on-disk YAML shape:
//
//	images:
//	  - nginx:1.27
//	  - quay.io/argoproj/argocd:v3.2.6
type listFile struct {
	Images []string `yaml:"images"`
}

// Load reads an image list from path. A missing or unreadable file, a malformed
// document, an unknown key, an invalid reference, or an empty list is an error:
// the list is the tool's trust boundary, so it fails closed before any bytes
// are pulled.
func Load(path string) ([]Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read image list: %w", err)
	}
	imgs, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("image list %s: %w", path, err)
	}
	return imgs, nil
}

// Parse decodes an image list document, validating and de-duplicating it. Order
// is preserved. Duplicates are collapsed by canonical pull reference, so two
// spellings of the same image ("nginx:1.27" and "docker.io/library/nginx:1.27")
// yield one entry — pulling both would write the same tarball twice.
func Parse(data []byte) ([]Image, error) {
	var parsed listFile
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&parsed); err != nil && !errors.Is(err, io.EOF) {
		// Unknown top-level keys usually mean the config file was passed to
		// -images: both are YAML, so lead with the shape we expect rather than
		// burying it under a dump of every offending field.
		var typeErr *yaml.TypeError
		if errors.As(err, &typeErr) {
			return nil, fmt.Errorf("parse: expected a YAML document with a top-level 'images:' key (see images.example.yaml): %w", err)
		}
		return nil, fmt.Errorf("parse: %w", err)
	}
	if len(parsed.Images) == 0 {
		return nil, fmt.Errorf("no images listed (expected an 'images:' list)")
	}
	out := make([]Image, 0, len(parsed.Images))
	seen := make(map[string]struct{}, len(parsed.Images))
	for i, raw := range parsed.Images {
		ref := strings.TrimSpace(raw)
		if ref == "" {
			return nil, fmt.Errorf("entry %d is empty", i+1)
		}
		if !ValidRef(ref) {
			return nil, fmt.Errorf("entry %d: invalid image reference %q", i+1, ref)
		}
		key := PullRef(ref)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, Image{Ref: ref, Selected: true})
	}
	return out, nil
}

// ValidateDest reports whether the given images retag to distinct, valid
// destination references under prefix. Two sources that mirror to the same
// destination would overwrite each other on the airgapped side (docker load
// keeps only the last tag), silently dropping an image from the bundle's load
// path. A destination that is not a parseable tag reference — typically a
// malformed registry prefix — is rejected here so the run fails at startup
// instead of after a full download.
func ValidateDest(imgs []Image, prefix string) error {
	seen := make(map[string]string, len(imgs))
	for _, img := range imgs {
		dest := Retag(img.Ref, prefix)
		if _, err := name.NewTag(dest); err != nil {
			return fmt.Errorf("image %q retags to invalid destination %q: %w", img.Ref, dest, err)
		}
		if prev, ok := seen[dest]; ok {
			return fmt.Errorf("images %q and %q both retag to %q; give one a distinct tag or a distinct registry prefix",
				prev, img.Ref, dest)
		}
		seen[dest] = img.Ref
	}
	return nil
}

// Refs returns the references of the given images.
func Refs(imgs []Image) []string {
	refs := make([]string, 0, len(imgs))
	for _, img := range imgs {
		refs = append(refs, img.Ref)
	}
	return refs
}

// defaultTag is used for the destination tag when a source reference carries no
// tag (e.g. it is pinned only by digest). A docker-style tarball must be tagged,
// and a digest cannot serve as a tag, so we fall back to "latest".
const defaultTag = "latest"

// splitRef separates an image reference into its repository name, tag, and
// digest. The tag and digest are returned without their ":"/"@" separators and
// are empty when absent. A reference may carry a tag, a digest, or both
// ("repo:tag@sha256:..."); the digest is parsed first so a registry port colon
// is not mistaken for a tag separator.
func splitRef(ref string) (repo, tag, digest string) {
	repo = strings.TrimSpace(ref)
	if at := strings.Index(repo, "@"); at >= 0 {
		digest = repo[at+1:]
		repo = repo[:at]
	}
	if colon := strings.LastIndex(repo, ":"); colon >= 0 && !strings.Contains(repo[colon+1:], "/") {
		tag = repo[colon+1:]
		repo = repo[:colon]
	}
	return repo, tag, digest
}

// normalizeName expands Docker Hub shorthand in a repository name into a
// fully-qualified name, e.g. "redis" => "docker.io/library/redis" and
// "mattermost/app" => "docker.io/mattermost/app". Names that already carry a
// registry host are returned unchanged.
func normalizeName(name string) string {
	first, _, _ := strings.Cut(name, "/")
	hasRegistry := strings.ContainsAny(first, ".:") || first == "localhost"
	if !hasRegistry {
		if !strings.Contains(name, "/") {
			name = "library/" + name
		}
		name = "docker.io/" + name
	}
	return name
}

// isImageRef applies light heuristics to reject values that are clearly not
// image references (empty strings, templated leftovers, plain words).
func isImageRef(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.ContainsAny(ref, " \t\n{}") {
		return false
	}
	// A real reference carries a tag, a digest, or a registry path separator.
	return strings.Contains(ref, ":") || strings.Contains(ref, "@") || strings.Contains(ref, "/")
}

// ValidRef reports whether ref is acceptable as a container image reference
// for pull/retag. It applies light heuristics, rejects refs with no repository
// name (e.g. ":" or "@sha256:..."), then rejects refs that cannot be normalized
// for pull (name.ParseReference).
func ValidRef(ref string) bool {
	ref = strings.TrimSpace(ref)
	if !isImageRef(ref) {
		return false
	}
	if repo, _, _ := splitRef(ref); strings.Trim(repo, "/") == "" {
		return false
	}
	_, err := name.ParseReference(PullRef(ref), name.WeakValidation)
	return err == nil
}

// PullRef returns the canonical reference used to pull an image. A digest is
// preferred over a tag for precision, and the two are never combined because
// registry clients reject "repo:tag@digest". Docker Hub shorthand is expanded.
func PullRef(ref string) string {
	repo, tag, digest := splitRef(ref)
	repo = normalizeName(repo)
	switch {
	case digest != "":
		return repo + "@" + digest
	case tag != "":
		return repo + ":" + tag
	default:
		return repo
	}
}

// Retag computes the destination reference for an image when mirrored behind
// prefix. The original registry path is preserved so the layout is predictable,
// e.g. prefix="rgy01.domain.local" + "quay.io/argoproj/argocd:v3.2.6"
// => "rgy01.domain.local/quay.io/argoproj/argocd:v3.2.6".
// Docker Hub shorthand like "redis:8" is normalized to "docker.io/library/...".
//
// The destination is always a tag reference: any digest is dropped (a tarball
// cannot be tagged by digest) and references without a tag fall back to
// "latest". A "repo:tag@digest" source therefore mirrors to "prefix/repo:tag".
func Retag(ref, prefix string) string {
	repo, tag, _ := splitRef(ref)
	repo = normalizeName(repo)
	if tag == "" {
		tag = defaultTag
	}
	dest := repo + ":" + tag
	prefix = strings.TrimRight(prefix, "/")
	if prefix == "" {
		return dest
	}
	return prefix + "/" + dest
}

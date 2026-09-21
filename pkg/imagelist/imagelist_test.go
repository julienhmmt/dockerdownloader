package imagelist_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/julienhmmt/dockerdownloader/pkg/imagelist"
)

// sha256Hex is a well-formed digest, so digest-pinned refs validate.
const sha256Hex = "sha256:6c3c624b58dbbcd3c0dd82b4c53f04194d1247c6eebdaab7c610cf7d66709b3b"

func TestParse_ValidList(t *testing.T) {
	imgs, err := imagelist.Parse([]byte(`
images:
  - nginx:1.27
  - quay.io/argoproj/argocd:v3.2.6
  - ghcr.io/org/app@` + sha256Hex + `
`))
	require.NoError(t, err)
	require.Len(t, imgs, 3)
	assert.Equal(t, "nginx:1.27", imgs[0].Ref)
	assert.Equal(t, "quay.io/argoproj/argocd:v3.2.6", imgs[1].Ref)
	assert.Equal(t, "ghcr.io/org/app@"+sha256Hex, imgs[2].Ref)
	for _, img := range imgs {
		assert.True(t, img.Selected, "entries default to selected")
	}
}

func TestParse_PreservesOrder(t *testing.T) {
	imgs, err := imagelist.Parse([]byte("images:\n  - b:2\n  - a:1\n  - c:3\n"))
	require.NoError(t, err)
	assert.Equal(t, []string{"b:2", "a:1", "c:3"}, imagelist.Refs(imgs))
}

func TestParse_TrimsWhitespace(t *testing.T) {
	imgs, err := imagelist.Parse([]byte("images:\n  - \"  nginx:1.27  \"\n"))
	require.NoError(t, err)
	require.Len(t, imgs, 1)
	assert.Equal(t, "nginx:1.27", imgs[0].Ref)
}

func TestParse_RejectsEmptyList(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{"empty document", ""},
		{"empty list", "images: []\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := imagelist.Parse([]byte(tt.data))
			assert.ErrorContains(t, err, "no images listed")
		})
	}
}

func TestParse_RejectsUnknownKey(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{"singular key typo", "image:\n  - nginx:1.27\n"},
		{"unrelated key", "something_else: 1\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := imagelist.Parse([]byte(tt.data))
			assert.ErrorContains(t, err, "parse")
		})
	}
}

func TestParse_RejectsInvalidRefs(t *testing.T) {
	tests := []struct {
		name string
		ref  string
	}{
		{"empty entry", ""},
		{"plain word", "nginx"},
		{"has spaces", "not an image"},
		{"unclosed template", "image:{{ .Values.tag }}"},
		{"bare colon", ":"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := imagelist.Parse([]byte("images:\n  - " + quote(tt.ref) + "\n"))
			want := "invalid image reference"
			if tt.ref == "" {
				want = "is empty"
			}
			assert.ErrorContains(t, err, want)
		})
	}
}

// quote wraps s in YAML double quotes so an entry with a leading ":" or an
// empty value still parses as a string.
func quote(s string) string { return `"` + s + `"` }

func TestParse_DedupesByCanonicalRef(t *testing.T) {
	// "nginx:1.27" and "docker.io/library/nginx:1.27" are the same image; both
	// would write the same tarball, so only the first spelling is kept.
	imgs, err := imagelist.Parse([]byte(`
images:
  - nginx:1.27
  - docker.io/library/nginx:1.27
  - redis:7
`))
	require.NoError(t, err)
	require.Len(t, imgs, 2)
	assert.Equal(t, "nginx:1.27", imgs[0].Ref)
	assert.Equal(t, "redis:7", imgs[1].Ref)
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := imagelist.Load(filepath.Join(t.TempDir(), "nope.yaml"))
	assert.ErrorContains(t, err, "read image list")
}

func TestLoad_ReadsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "images.yaml")
	require.NoError(t, os.WriteFile(path, []byte("images:\n  - nginx:1.27\n"), 0o644))
	imgs, err := imagelist.Load(path)
	require.NoError(t, err)
	require.Len(t, imgs, 1)
	assert.Equal(t, "nginx:1.27", imgs[0].Ref)
}

func TestLoad_ExampleListParses(t *testing.T) {
	// images.example.yaml must stay valid: the list is the trust boundary, so a
	// stale example would fail on a user's first run.
	imgs, err := imagelist.Load(filepath.Join("..", "..", "images.example.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, imgs)
}

func TestValidRef(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"nginx:1.27", true},
		{"nginx", false},
		{"quay.io/argoproj/argocd:v3.2.6", true},
		{"ghcr.io/org/app@" + sha256Hex, true},
		{":", false},
		{"@" + sha256Hex, false},
		{"ghcr.io/org/app@sha256:abc", false},
		{"localhost:5000/app:1", true},
		{"", false},
		{"has space", false},
		{"{{ .Values.image }}", false},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			assert.Equal(t, tt.want, imagelist.ValidRef(tt.ref))
		})
	}
}

func TestPullRef(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"nginx:1.27", "docker.io/library/nginx:1.27"},
		{"redis", "docker.io/library/redis"},
		{"mattermost/app:1", "docker.io/mattermost/app:1"},
		{"quay.io/argoproj/argocd:v3.2.6", "quay.io/argoproj/argocd:v3.2.6"},
		{"localhost:5000/app:1", "localhost:5000/app:1"},
		// A digest wins over a tag: registries reject "repo:tag@digest".
		{"ghcr.io/org/app:v1@" + sha256Hex, "ghcr.io/org/app@" + sha256Hex},
		{"ghcr.io/org/app@" + sha256Hex, "ghcr.io/org/app@" + sha256Hex},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			assert.Equal(t, tt.want, imagelist.PullRef(tt.ref))
		})
	}
}

func TestRetag(t *testing.T) {
	tests := []struct {
		name   string
		ref    string
		prefix string
		want   string
	}{
		{"no prefix keeps path", "quay.io/argoproj/argocd:v3.2.6", "", "quay.io/argoproj/argocd:v3.2.6"},
		{"prefix prepended", "quay.io/argoproj/argocd:v3.2.6", "rgy01.domain.local", "rgy01.domain.local/quay.io/argoproj/argocd:v3.2.6"},
		{"trailing slash trimmed", "redis:7", "rgy01.domain.local/", "rgy01.domain.local/docker.io/library/redis:7"},
		{"shorthand expanded", "redis:7", "r.local", "r.local/docker.io/library/redis:7"},
		{"digest dropped", "ghcr.io/org/app@" + sha256Hex, "r.local", "r.local/ghcr.io/org/app:latest"},
		{"tag kept with digest", "ghcr.io/org/app:v1@" + sha256Hex, "r.local", "r.local/ghcr.io/org/app:v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, imagelist.Retag(tt.ref, tt.prefix))
		})
	}
}

func TestValidateDest_RejectsCollision(t *testing.T) {
	// Two digests of one repo both retag to <prefix>/repo:latest, so docker
	// load would keep only the last tag and silently drop an image.
	imgs := []imagelist.Image{
		{Ref: "ghcr.io/org/app@sha256:" + strings.Repeat("a", 64)},
		{Ref: "ghcr.io/org/app@sha256:" + strings.Repeat("b", 64)},
	}
	err := imagelist.ValidateDest(imgs, "r.local")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both retag to")
	assert.Contains(t, err.Error(), "r.local/ghcr.io/org/app:latest")
}

func TestValidateDest_AcceptsDistinctTags(t *testing.T) {
	imgs := []imagelist.Image{
		{Ref: "ghcr.io/org/app:1"},
		{Ref: "ghcr.io/org/app:2"},
	}
	assert.NoError(t, imagelist.ValidateDest(imgs, "r.local"))
}

func TestValidateDest_RejectsInvalidPrefix(t *testing.T) {
	// A malformed registry prefix must fail at startup, not per-image after a
	// full download.
	imgs := []imagelist.Image{{Ref: "ghcr.io/org/app:1"}}
	for _, prefix := range []string{"r local", "r.local:notaport", "r.local/TEAM"} {
		t.Run(prefix, func(t *testing.T) {
			err := imagelist.ValidateDest(imgs, prefix)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid destination")
		})
	}
}

func TestValidateDest_SameRepoDistinctPrefixes(t *testing.T) {
	imgs := []imagelist.Image{{Ref: "ghcr.io/org/app:1"}}
	assert.NoError(t, imagelist.ValidateDest(imgs, ""))
}

func TestRefs(t *testing.T) {
	imgs := []imagelist.Image{{Ref: "a:1"}, {Ref: "b:2"}}
	assert.Equal(t, []string{"a:1", "b:2"}, imagelist.Refs(imgs))
	assert.Empty(t, imagelist.Refs(nil))
}

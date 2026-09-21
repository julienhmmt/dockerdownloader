package bundle

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedTime is a deterministic timestamp for SBOM tests.
var fixedTime = time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)

func TestBuildSBOM_HasRequiredFields(t *testing.T) {
	spec := Spec{
		Name: "prod-images",
		Images: []ImageEntry{
			{SourceRef: "quay.io/x:1", DestRef: "rgy.local/x:1", Digest: "sha256:aaa"},
			{SourceRef: "redis:7", DestRef: "rgy.local/redis:7"},
		},
	}
	out, err := buildSBOM(spec, fixedTime)
	require.NoError(t, err)
	var doc spdxDocument
	require.NoError(t, json.Unmarshal(out, &doc))
	assert.Equal(t, "SPDX-2.3", doc.SPDXVersion)
	assert.Equal(t, "CC0-1.0", doc.DataLicense)
	assert.Equal(t, "SPDXRef-DOCUMENT", doc.SPDXID)
	assert.Contains(t, doc.Name, "prod-images")
	require.NotEmpty(t, doc.CreationInfo.Created)
	require.NotEmpty(t, doc.CreationInfo.Creators)
	assert.Contains(t, doc.CreationInfo.Creators[0], "dockerdownloader")
	// One package per image, each described by the document.
	require.Len(t, doc.Packages, 2)
	assert.Equal(t, "SPDXRef-Package-Image-1", doc.Packages[0].SPDXID)
	assert.Equal(t, "quay.io/x:1", doc.Packages[0].Name)
	assert.Len(t, doc.Relationships, 2)
	for _, rel := range doc.Relationships {
		assert.Equal(t, "SPDXRef-DOCUMENT", rel.SPDXElementID)
		assert.Equal(t, "DESCRIBES", rel.RelationshipType)
		assert.True(t, strings.HasPrefix(rel.RelatedSPDXElement, "SPDXRef-Package-Image-"))
	}
}

func TestBuildSBOM_PurlIsWellFormed(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"tagged ref", "nginx:1.27", "pkg:oci/nginx@1.27?repository_url=nginx"},
		{"digest-pinned ref", "ghcr.io/dexidp/dex@sha256:abc", "pkg:oci/dex@sha256:abc?repository_url=ghcr.io/dexidp/dex"},
		{"registry path with tag", "quay.io/argoproj/argocd:v3.2.6", "pkg:oci/argocd@v3.2.6?repository_url=quay.io/argoproj/argocd"},
		{"digest preferred over tag", "docker.io/library/nginx:1.27@sha256:def", "pkg:oci/nginx@sha256:def?repository_url=docker.io/library/nginx"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := Spec{Name: "c", Images: []ImageEntry{{SourceRef: tc.source, Digest: "sha256:abc"}}}
			out, err := buildSBOM(spec, fixedTime)
			require.NoError(t, err)
			var doc spdxDocument
			require.NoError(t, json.Unmarshal(out, &doc), "document must still unmarshal")
			require.Len(t, doc.Packages, 1)
			require.Len(t, doc.Packages[0].ExternalRefs, 1)
			ref := doc.Packages[0].ExternalRefs[0]
			assert.Equal(t, "PACKAGE-MANAGER", ref.ReferenceCategory)
			assert.Equal(t, "purl", ref.ReferenceType)
			assert.Equal(t, tc.want, ref.ReferenceLocator)
		})
	}
}

func TestBuildSBOM_ImageDigestAsChecksum(t *testing.T) {
	spec := Spec{
		Name:   "c",
		Images: []ImageEntry{{SourceRef: "x:1", Digest: "sha256:abc"}},
	}
	out, err := buildSBOM(spec, fixedTime)
	require.NoError(t, err)
	var doc spdxDocument
	require.NoError(t, json.Unmarshal(out, &doc))
	require.Len(t, doc.Packages, 1)
	imgPkg := doc.Packages[0]
	require.Len(t, imgPkg.Checksums, 1)
	assert.Equal(t, "SHA256", imgPkg.Checksums[0].Algorithm)
	assert.Equal(t, "abc", imgPkg.Checksums[0].ChecksumValue)
}

func TestBuildSBOM_NoDigestOmitsChecksum(t *testing.T) {
	spec := Spec{
		Name:   "c",
		Images: []ImageEntry{{SourceRef: "x:1", Digest: ""}},
	}
	out, err := buildSBOM(spec, fixedTime)
	require.NoError(t, err)
	var doc spdxDocument
	require.NoError(t, json.Unmarshal(out, &doc))
	require.Len(t, doc.Packages, 1)
	assert.Empty(t, doc.Packages[0].Checksums, "image with no digest must omit checksums")
}

func TestBuildSBOM_DigestDashSentinelOmitsChecksum(t *testing.T) {
	spec := Spec{
		Name:   "c",
		Images: []ImageEntry{{SourceRef: "x:1", Digest: "-"}},
	}
	out, err := buildSBOM(spec, fixedTime)
	require.NoError(t, err)
	var doc spdxDocument
	require.NoError(t, json.Unmarshal(out, &doc))
	require.Len(t, doc.Packages, 1)
	assert.Empty(t, doc.Packages[0].Checksums, "image with '-' digest sentinel must omit checksums")
}

func TestBuildSBOM_ValidJSON(t *testing.T) {
	spec := Spec{
		Name:   "c",
		Images: []ImageEntry{{SourceRef: "x:1", Digest: "sha256:abc"}},
	}
	out, err := buildSBOM(spec, fixedTime)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(out, &raw), "buildSBOM output must be valid JSON")
	assert.Equal(t, "SPDX-2.3", raw["spdxVersion"])
}

func TestCreate_IncludesSBOM(t *testing.T) {
	work := t.TempDir()
	out := t.TempDir()
	img := writeTemp(t, work, "i.tar", "tar")
	path, err := Create(Spec{
		Name:      "c",
		OutputDir: out,
		Images:    []ImageEntry{{TarPath: img, SourceRef: "x:1", DestRef: "r/x:1", Digest: "sha256:abc"}},
	})
	require.NoError(t, err)
	contents, _ := readArchive(t, path)
	sbom, ok := contents["sbom.spdx.json"]
	require.True(t, ok, "bundle must contain sbom.spdx.json")
	var doc spdxDocument
	require.NoError(t, json.Unmarshal([]byte(sbom), &doc), "sbom.spdx.json must be valid JSON")
	assert.Equal(t, "SPDX-2.3", doc.SPDXVersion)
	// sha256sums.txt must cover the SBOM.
	assert.Contains(t, contents["sha256sums.txt"], "  sbom.spdx.json")
}

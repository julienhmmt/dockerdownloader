package bundle

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/julienhmmt/dockerdownloader/pkg/version"
)

// SPDX 2.3 JSON document structures. Only the fields needed for standard
// SBOM tooling ingestion are modeled; the format is documented at
// https://spdx.github.io/spdx-spec/v2.3/.

type spdxDocument struct {
	SPDXVersion       string         `json:"spdxVersion"`
	DataLicense       string         `json:"dataLicense"`
	SPDXID            string         `json:"SPDXID"`
	Name              string         `json:"name"`
	DocumentNamespace string         `json:"documentNamespace"`
	CreationInfo      spdxCreation   `json:"creationInfo"`
	Packages          []spdxPackage  `json:"packages"`
	Relationships     []spdxRelation `json:"relationships"`
}

type spdxCreation struct {
	Created            string   `json:"created"`
	Creators           []string `json:"creators"`
	LicenseListVersion string   `json:"licenseListVersion,omitempty"`
}

type spdxPackage struct {
	SPDXID           string            `json:"SPDXID"`
	Name             string            `json:"name"`
	VersionInfo      string            `json:"versionInfo,omitempty"`
	DownloadLocation string            `json:"downloadLocation,omitempty"`
	FilesAnalyzed    bool              `json:"filesAnalyzed"`
	LicenseConcluded string            `json:"licenseConcluded,omitempty"`
	LicenseDeclared  string            `json:"licenseDeclared,omitempty"`
	Copyright        string            `json:"copyrightText,omitempty"`
	ExternalRefs     []spdxExternalRef `json:"externalRefs,omitempty"`
	Checksums        []spdxChecksum    `json:"checksums,omitempty"`
}

type spdxExternalRef struct {
	ReferenceCategory string `json:"referenceCategory"`
	ReferenceType     string `json:"referenceType"`
	ReferenceLocator  string `json:"referenceLocator"`
}

type spdxChecksum struct {
	Algorithm     string `json:"algorithm"`
	ChecksumValue string `json:"checksumValue"`
}

type spdxRelation struct {
	SPDXElementID      string `json:"spdxElementId"`
	RelationshipType   string `json:"relationshipType"`
	RelatedSPDXElement string `json:"relatedSpdxElement"`
}

// buildSBOM renders an SPDX 2.3 JSON document for the bundle: one package per
// container image, each carrying its pinned digest as a checksum when present.
// now supplies the creation timestamp so tests can control it. The returned
// bytes are pretty-printed JSON.
func buildSBOM(spec Spec, now time.Time) ([]byte, error) {
	docNS := fmt.Sprintf("https://dockerdownloader.example/spdx/%s-%d",
		spec.Name, now.Unix())
	doc := spdxDocument{
		SPDXVersion:       "SPDX-2.3",
		DataLicense:       "CC0-1.0",
		SPDXID:            "SPDXRef-DOCUMENT",
		Name:              fmt.Sprintf("dockerdownloader bundle %s", spec.Name),
		DocumentNamespace: docNS,
		CreationInfo: spdxCreation{
			Created:  now.UTC().Format(time.RFC3339),
			Creators: []string{"Tool: " + version.String()},
		},
	}
	for i, img := range spec.Images {
		pkgID := fmt.Sprintf("SPDXRef-Package-Image-%d", i+1)
		imgPkg := spdxPackage{
			SPDXID:           pkgID,
			Name:             img.SourceRef,
			DownloadLocation: img.SourceRef,
			FilesAnalyzed:    false,
			LicenseConcluded: "NOASSERTION",
			LicenseDeclared:  "NOASSERTION",
			Copyright:        "NOASSERTION",
			ExternalRefs: []spdxExternalRef{
				{
					ReferenceCategory: "PACKAGE-MANAGER",
					ReferenceType:     "purl",
					ReferenceLocator:  imagePURL(img),
				},
			},
		}
		if img.Digest != "" && img.Digest != "-" {
			algo, val := parseDigest(img.Digest)
			imgPkg.Checksums = []spdxChecksum{{Algorithm: algo, ChecksumValue: val}}
		}
		doc.Packages = append(doc.Packages, imgPkg)
		doc.Relationships = append(doc.Relationships, spdxRelation{
			SPDXElementID:      "SPDXRef-DOCUMENT",
			RelationshipType:   "DESCRIBES",
			RelatedSPDXElement: pkgID,
		})
	}
	return json.MarshalIndent(doc, "", "  ")
}

// parseDigest splits a "sha256:hex" digest into SPDX checksum algorithm and
// value. SPDX uses uppercase algorithm names ("SHA256"). Unknown algorithms
// fall back to the raw string.
func parseDigest(d string) (algo, value string) {
	if i := strings.Index(d, ":"); i >= 0 {
		return strings.ToUpper(d[:i]), d[i+1:]
	}
	return "SHA256", d
}

// imagePURL renders a valid package-url for an OCI image. A purl requires an
// "@version" component, so the pinned digest is used when present (it is the
// precise identity of the image) and the tag is the fallback; the repository
// path is carried as the repository_url qualifier so the reference stays
// unambiguous. The previous "pkg:oci/<sourceRef>" form put a tag inside the
// name, which strict purl consumers reject.
func imagePURL(img ImageEntry) string {
	repo, tag, digest := splitImageRef(img.SourceRef)
	version := digest
	if version == "" {
		version = tag
	}
	name := repo
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		name = repo[i+1:]
	}
	purl := "pkg:oci/" + name
	if version != "" {
		purl += "@" + version
	}
	if repo != "" {
		purl += "?repository_url=" + repo
	}
	return purl
}

// splitImageRef separates an image reference into repository, tag, and digest.
// The tag and digest are returned without their separators. A registry port
// colon is not mistaken for a tag separator.
func splitImageRef(ref string) (repo, tag, digest string) {
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

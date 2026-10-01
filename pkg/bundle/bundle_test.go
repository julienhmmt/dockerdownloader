package bundle

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTemp creates a file with content under dir and returns its path.
func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// readArchive returns a map of archive entry name to its content and a map of
// name to its tar mode.
func readArchive(t *testing.T, path string) (map[string]string, map[string]int64) {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { assert.NoError(t, f.Close()) }()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	tr := tar.NewReader(gz)
	contents := map[string]string{}
	modes := map[string]int64{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		data, err := io.ReadAll(tr)
		require.NoError(t, err)
		contents[hdr.Name] = string(data)
		modes[hdr.Name] = hdr.Mode
	}
	return contents, modes
}

func TestCreate_WritesAllEntries(t *testing.T) {
	work := t.TempDir()
	out := t.TempDir()
	img1 := writeTemp(t, work, "img1.tar", "tar1")
	img2 := writeTemp(t, work, "img2.tar", "tar2")

	path, err := Create(Spec{
		Name:      "prod-images",
		Platform:  "linux/amd64",
		OutputDir: out,
		Images: []ImageEntry{
			{TarPath: img1, SourceRef: "quay.io/x:1", DestRef: "rgy.local/quay.io/x:1", Digest: "sha256:aaa"},
			{TarPath: img2, SourceRef: "redis:7", DestRef: "rgy.local/docker.io/library/redis:7"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(out, "prod-images-bundle.tar.gz"), path)

	contents, modes := readArchive(t, path)
	assert.Equal(t, "tar1", contents["images/img1.tar"])
	assert.Equal(t, "tar2", contents["images/img2.tar"])
	assert.Contains(t, contents, "images.txt")
	assert.Contains(t, contents, "manifest.json")
	assert.Contains(t, contents, "sbom.spdx.json")
	assert.Contains(t, contents, "load.sh")
	assert.Equal(t, int64(0o755), modes["load.sh"], "load.sh must be executable")

	// images.txt records source, dest, tar name, and digest (or "-" when absent).
	manifest := contents["images.txt"]
	assert.Contains(t, manifest, "quay.io/x:1\trgy.local/quay.io/x:1\timages/img1.tar\tsha256:aaa")
	assert.Contains(t, manifest, "images/img2.tar\t-")
	// A known digest is emitted as a comment above its load_and_push line.
	assert.Contains(t, contents["load.sh"], "# sha256:aaa")

	// sha256sums.txt covers every payload file including load.sh (not itself).
	sums := contents["sha256sums.txt"]
	assert.Contains(t, sums, "  images/img1.tar")
	assert.Contains(t, sums, "  images.txt")
	assert.Contains(t, sums, "  manifest.json")
	assert.Contains(t, sums, "  sbom.spdx.json")
	assert.Contains(t, sums, "  load.sh")
	// Checksums must match the actual bundled bytes.
	for line := range strings.SplitSeq(strings.TrimSpace(sums), "\n") {
		parts := strings.SplitN(line, "  ", 2)
		require.Len(t, parts, 2, "malformed sum line %q", line)
		sum := sha256.Sum256([]byte(contents[parts[1]]))
		assert.Equal(t, hex.EncodeToString(sum[:]), parts[0], "checksum mismatch for %s", parts[1])
	}
	// load.sh verifies before pushing and fails closed without a checksum tool.
	assert.Contains(t, contents["load.sh"], "sha256sums.txt")
	assert.Contains(t, contents["load.sh"], "refuse to load without integrity check")

	// manifest.json provenance is present and references the images.
	assert.Contains(t, contents["manifest.json"], `"tool": "dockerdownloader"`)
	assert.Contains(t, contents["manifest.json"], `"toolVersion":`)
	assert.Contains(t, contents["manifest.json"], `"platform": "linux/amd64"`)
	assert.Contains(t, contents["manifest.json"], "sha256:aaa")
}

func TestCreate_RebuildPreservesBundleOnFailure(t *testing.T) {
	for _, compression := range []string{"gzip", "zstd"} {
		t.Run(compression, func(t *testing.T) {
			work := t.TempDir()
			out := t.TempDir()
			spec := Spec{Name: "rebuild", OutputDir: out, Compression: compression,
				Images: []ImageEntry{{TarPath: writeTemp(t, work, "i.tar", "original"), SourceRef: "x:1", DestRef: "r/x:1", Digest: "sha256:abc"}}}
			path, err := Create(spec)
			require.NoError(t, err)
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			spec.Images[0].TarPath = filepath.Join(work, "missing.tar")
			_, err = Create(spec)
			require.Error(t, err)
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after)
			assert.NoError(t, Verify(path))
			files, err := os.ReadDir(out)
			require.NoError(t, err)
			assert.Len(t, files, 1, "failed rebuild must not leave a temporary archive")
			spec.Images[0].TarPath = writeTemp(t, work, "i.tar", "replacement")
			rebuilt, err := Create(spec)
			require.NoError(t, err)
			assert.Equal(t, path, rebuilt)
			after, err = os.ReadFile(path)
			require.NoError(t, err)
			assert.NotEqual(t, before, after)
			assert.NoError(t, Verify(path))
		})
	}
}

func TestCreate_PublicationFailureCleansTemporaryArchive(t *testing.T) {
	out := t.TempDir()
	destination := filepath.Join(out, "images-bundle.tar.gz")
	require.NoError(t, os.Mkdir(destination, 0o755))
	_, err := Create(Spec{Name: "images", OutputDir: out, Images: []ImageEntry{
		{TarPath: writeTemp(t, t.TempDir(), "i.tar", "image"), SourceRef: "x:1", DestRef: "r/x:1"},
	}})
	require.Error(t, err)
	files, err := os.ReadDir(out)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.True(t, files[0].IsDir())
}

func TestCreate_NoImagesRejected(t *testing.T) {
	_, err := Create(Spec{Name: "empty", OutputDir: t.TempDir()})
	assert.ErrorContains(t, err, "no images to bundle")
}

func TestCreate_SanitizesBundleName(t *testing.T) {
	work := t.TempDir()
	out := t.TempDir()
	img := writeTemp(t, work, "i.tar", "tar")

	path, err := Create(Spec{
		Name:      "../../etc/passwd",
		OutputDir: out,
		Images:    []ImageEntry{{TarPath: img, SourceRef: "x:1", DestRef: "r/x:1"}},
	})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(out, "etc_passwd-bundle.tar.gz"), path)
	assert.Equal(t, out, filepath.Dir(path), "the archive must stay inside OutputDir")
	assert.NotContains(t, filepath.Base(path), "..")
}

func TestCreate_ZstdProducesZstExtension(t *testing.T) {
	work := t.TempDir()
	out := t.TempDir()
	img := writeTemp(t, work, "i.tar", "tar")

	path, err := Create(Spec{
		Name:        "c",
		OutputDir:   out,
		Compression: "zstd",
		Images:      []ImageEntry{{TarPath: img, SourceRef: "redis:7", DestRef: "rgy.local/redis:7"}},
	})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(out, "c-bundle.tar.zst"), path)

	// The archive must be readable back through the zstd decoder.
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { assert.NoError(t, f.Close()) }()
	zr, err := zstd.NewReader(f)
	require.NoError(t, err)
	defer zr.Close()
	tr := tar.NewReader(zr)
	var names []string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		names = append(names, hdr.Name)
	}
	assert.Contains(t, names, "images/i.tar")
	assert.Contains(t, names, "load.sh")
}

func TestCreate_RejectsUnknownCompression(t *testing.T) {
	_, err := Create(Spec{
		Name: "c", OutputDir: t.TempDir(), Compression: "lzma",
		Images: []ImageEntry{{TarPath: writeTemp(t, t.TempDir(), "i.tar", "y"), DestRef: "r/x:1"}},
	})
	assert.ErrorContains(t, err, "unknown compression")
}

func TestCreate_LoadScriptListsImages(t *testing.T) {
	work := t.TempDir()
	out := t.TempDir()
	img := writeTemp(t, work, "i.tar", "y")

	path, err := Create(Spec{
		Name:      "c",
		OutputDir: out,
		Images:    []ImageEntry{{TarPath: img, SourceRef: "redis:7", DestRef: "rgy.local/redis:7"}},
	})
	require.NoError(t, err)
	contents, _ := readArchive(t, path)
	script := contents["load.sh"]
	assert.True(t, strings.HasPrefix(script, "#!/bin/sh\n"))
	assert.Contains(t, script, "load_and_push 'images/i.tar' 'rgy.local/redis:7'")
	assert.Contains(t, script, `ENGINE="${ENGINE:-docker}"`)
}

func TestBuildLoadScript_QuotesAndCountsImages(t *testing.T) {
	script := buildLoadScript([]ImageEntry{
		{TarPath: "/work/images/a.tar", DestRef: "rgy.local/a:1"},
		{TarPath: "/work/images/b.tar", DestRef: "rgy.local/b:2"},
	})
	assert.Contains(t, script, "load_and_push 'images/a.tar' 'rgy.local/a:1'")
	assert.Contains(t, script, "load_and_push 'images/b.tar' 'rgy.local/b:2'")
	assert.Contains(t, script, `"$ENGINE" load -i "$DIR/$1"`)
	assert.Contains(t, script, `"$ENGINE" push "$2"`)
	assert.Contains(t, script, "2 image(s)")
	// DRY_RUN preview support and unconditional load-before-push.
	assert.Contains(t, script, `DRY_RUN="${DRY_RUN:-}"`)
	assert.Contains(t, script, `echo "DRY_RUN: $*"`)
	assert.NotContains(t, script, `"$ENGINE" image inspect "$2"`)
	assert.NotContains(t, script, "already present, skipping load")
	// Fail closed when no checksum tool is available.
	assert.Contains(t, script, "refuse to load without integrity check")
	assert.Contains(t, script, "exit 1")
	assert.NotContains(t, script, "skipping checksum verification")
}

func TestCreate_ChecksumsIncludeLoadSh(t *testing.T) {
	work := t.TempDir()
	out := t.TempDir()
	img := writeTemp(t, work, "i.tar", "tar")
	path, err := Create(Spec{
		Name:      "c",
		OutputDir: out,
		Images:    []ImageEntry{{TarPath: img, SourceRef: "x:1", DestRef: "r/x:1", Digest: "sha256:abc"}},
	})
	require.NoError(t, err)
	contents, modes := readArchive(t, path)
	require.Contains(t, contents, "load.sh")
	require.Contains(t, contents, "sha256sums.txt")
	assert.Equal(t, int64(0o755), modes["load.sh"])
	sums := contents["sha256sums.txt"]
	assert.Contains(t, sums, "  load.sh")
	loadSum := sha256.Sum256([]byte(contents["load.sh"]))
	wantLine := hex.EncodeToString(loadSum[:]) + "  load.sh"
	assert.Contains(t, sums, wantLine)
}

func TestBuildLoadScript_IsValidShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	script := buildLoadScript([]ImageEntry{
		{TarPath: "/work/images/a.tar", DestRef: "rgy.local/a:1", Digest: "sha256:abc"},
		{TarPath: "/work/images/b.tar", DestRef: "rgy.local/b:2"},
	})
	path := writeTemp(t, t.TempDir(), "load.sh", script)
	out, err := exec.Command("sh", "-n", path).CombinedOutput()
	require.NoError(t, err, "sh -n failed: %s", out)
}

// TestBuildLoadScript_RefusesWithoutChecksums runs the generated script in a
// directory with no sha256sums.txt: it must fail closed instead of loading
// unverified images, which is the downgrade an attacker would aim for.
func TestBuildLoadScript_RefusesWithoutChecksums(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	script := buildLoadScript([]ImageEntry{{TarPath: "/work/images/a.tar", DestRef: "rgy.local/a:1"}})
	path := writeTemp(t, dir, "load.sh", script)
	cmd := exec.Command("sh", path)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "ENGINE=true")
	out, err := cmd.CombinedOutput()
	require.Error(t, err, "script must fail closed without sha256sums.txt")
	assert.Contains(t, string(out), "missing sha256sums.txt")
}

func TestBuildLoadScript_AlwaysLoadsBeforePush(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	out := t.TempDir()
	path, err := Create(Spec{Name: "load", OutputDir: out, Images: []ImageEntry{
		{TarPath: writeTemp(t, t.TempDir(), "i.tar", "image"), SourceRef: "x:1", DestRef: "r/x:1"},
	}})
	require.NoError(t, err)
	contents, _ := readArchive(t, path)
	for name, content := range contents {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(out, name)), 0o755))
		writeTemp(t, out, name, content)
	}
	engine := writeTemp(t, out, "engine", "#!/bin/sh\nif [ \"$1\" = image ]; then exit 0; fi\nprintf 'engine:%s\\n' \"$1\"\n")
	require.NoError(t, os.Chmod(engine, 0o755))
	for _, dryRun := range []string{"", "1"} {
		t.Run("dry-run="+dryRun, func(t *testing.T) {
			cmd := exec.Command("sh", filepath.Join(out, "load.sh"))
			cmd.Env = append(os.Environ(), "ENGINE="+engine, "DRY_RUN="+dryRun)
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)
			text := string(output)
			assert.NotContains(t, text, "skipping load")
			if dryRun != "" {
				assert.Contains(t, text, "DRY_RUN: "+engine+" load -i ")
				assert.Contains(t, text, "DRY_RUN: "+engine+" push r/x:1")
				assert.NotContains(t, text, "engine:")
				return
			}
			load := strings.Index(text, "engine:load")
			push := strings.Index(text, "engine:push")
			require.GreaterOrEqual(t, load, 0)
			assert.Greater(t, push, load)
		})
	}
}

func TestShellQuote_EscapesSingleQuotes(t *testing.T) {
	assert.Equal(t, `'plain'`, shellQuote("plain"))
	assert.Equal(t, `'a'\''b'`, shellQuote("a'b"))
}

// TestCreate_ReproducibleWithSourceDateEpoch pins SOURCE_DATE_EPOCH: two builds
// from identical inputs must produce byte-identical archives.
func TestCreate_ReproducibleWithSourceDateEpoch(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	build := func() []byte {
		work := t.TempDir()
		out := t.TempDir()
		img := writeTemp(t, work, "i.tar", "tar-bytes")
		path, err := Create(Spec{
			Name:      "c",
			OutputDir: out,
			Images:    []ImageEntry{{TarPath: img, SourceRef: "x:1", DestRef: "r/x:1", Digest: "sha256:abc"}},
		})
		require.NoError(t, err)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		return data
	}
	assert.Equal(t, build(), build(), "identical inputs must yield identical bundles")
}

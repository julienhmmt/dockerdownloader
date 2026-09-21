package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/julienhmmt/dockerdownloader/pkg/config"
)

func TestLoadImagesForTUI(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		return path
	}
	valid := write("valid.yaml", "images:\n  - nginx:1.27\n  - redis:7\n")
	empty := write("empty.yaml", "images: []\n")
	invalid := write("invalid.yaml", "images:\n  - not an image\n")
	unknownKey := write("unknown.yaml", "images:\n  - nginx:1.27\nbogus_key: true\n")
	missing := filepath.Join(dir, "missing.yaml")

	tests := []struct {
		name       string
		imagesFile string
		wantErr    bool
		wantNotice bool
		wantImages int
	}{
		{"missing file opens the TUI", missing, false, true, 0},
		{"valid list", valid, false, false, 2},
		{"present but empty still fails closed", empty, true, false, 0},
		{"invalid reference still fails closed", invalid, true, false, 0},
		{"unknown key still fails closed", unknownKey, true, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.ImagesFile = tt.imagesFile
			imgs, notice, err := loadImagesForTUI(cfg)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Len(t, imgs, tt.wantImages)
			assert.Equal(t, tt.wantNotice, notice != "")
		})
	}
}

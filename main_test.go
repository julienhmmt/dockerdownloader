package main

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/julienhmmt/dockerdownloader/pkg/config"
)

func TestCLI_Process(_ *testing.T) {
	if os.Getenv("DOCKERDOWNLOADER_TEST_CLI") != "1" {
		return
	}
	os.Args = append([]string{os.Args[0]}, flag.Args()...)
	flag.CommandLine = flag.NewFlagSet("dockerdownloader", flag.ExitOnError)
	main()
}

func runCLI(t *testing.T, args []string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestCLI_Process$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "DOCKERDOWNLOADER_TEST_CLI=1")
	return cmd.CombinedOutput()
}

func TestCLI_LogFilePrecedence(t *testing.T) {
	for _, value := range []string{"", "override.log", "dockerdownloader.log"} {
		t.Run("flag="+value, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.yaml")
			configured := filepath.Join(dir, "configured.log")
			require.NoError(t, os.WriteFile(configPath, []byte("verbose: true\nlog_file: "+configured+"\nwork_dir: "+dir+"\n"), 0o600))
			args := []string{"-config", configPath, "-images", dir}
			want := configured
			if value != "" {
				want = filepath.Join(dir, value)
				args = append(args, "-log-file", want)
			}
			output, err := runCLI(t, args)
			require.Error(t, err, "%s", output)
			assert.Contains(t, string(output), "read image list")
			_, err = os.Stat(want)
			assert.NoError(t, err, "logging must use the selected path")
			if value != "" {
				_, err = os.Stat(configured)
				assert.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}

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

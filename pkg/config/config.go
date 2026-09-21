// Package config defines the runtime configuration for dockerdownloader.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Supported TUI theme names.
const (
	ThemeAuto         = "auto"
	ThemeLight        = "light"
	ThemeDark         = "dark"
	ThemeHighContrast = "high-contrast"
	ThemeOcean        = "ocean"
	ThemeMatrix       = "matrix"
)

// ThemeMenu is the order of themes shown in the TUI theme picker (Ctrl+T).
var ThemeMenu = []string{
	ThemeAuto,
	ThemeLight,
	ThemeDark,
	ThemeHighContrast,
	ThemeOcean,
	ThemeMatrix,
}

// Config holds all tunable settings for the application.
type Config struct {
	// RegistryPrefix is prepended to every image reference when retagging,
	// e.g. "rgy01.domain.local". The airgapped registry images will be pushed to.
	RegistryPrefix string `yaml:"registry_prefix"`
	// RegistryAuth, when true, enables authenticated pulls using the default
	// Docker keychain (reads ~/.docker/config.json or $DOCKER_CONFIG/config.json,
	// and Podman's containers/auth.json). Use this to pull from private
	// registries; log in with `docker login` (or `podman login`) first.
	RegistryAuth bool `yaml:"registry_auth"`
	// Platform is the OS/arch the images are pulled for, e.g. "linux/amd64".
	Platform string `yaml:"platform"`
	// OutputDir is where bundles are written.
	OutputDir string `yaml:"output_dir"`
	// WorkDir is where intermediate files (image tarballs) are stored during
	// processing. If empty, a temporary directory is used.
	WorkDir string `yaml:"work_dir"`
	// TempDir is the parent directory used for temporary work directories
	// when WorkDir is empty. It is verified at startup; if not writable, a
	// warning is printed and a fallback is selected.
	TempDir string `yaml:"temp_dir"`
	// Concurrency is the maximum number of images downloaded in parallel.
	// Values below 1 are treated as 1.
	Concurrency int `yaml:"concurrency"`
	// Retries is the number of additional attempts made for a failed image
	// pull, on top of the initial try, using exponential backoff. Negative
	// values are treated as 0.
	Retries int `yaml:"retries"`
	// Compression selects the bundle archive codec: "gzip" (default) or "zstd"
	// for a smaller archive.
	Compression string `yaml:"compression"`
	// MinFreeDiskMB is the minimum free space, in MiB, required on the work
	// directory's filesystem before a download starts. 0 disables the check.
	MinFreeDiskMB int `yaml:"min_free_disk_mb"`
	// Resume, when true, reuses image tarballs already present in a persistent
	// work directory instead of pulling them again. Only meaningful with a
	// fixed work_dir; a temporary work dir is empty on each run.
	Resume bool `yaml:"resume"`
	// BundleName names the output archive: "<bundle_name>-bundle.tar.<ext>".
	BundleName string `yaml:"bundle_name"`
	// ImagesFile is the YAML list of image references to bundle. Used by the
	// TUI; the batch subcommand takes the list path as its argument instead.
	ImagesFile string `yaml:"images_file"`
	// HTTPSProxy, when set, is used for registry network calls.
	HTTPSProxy string `yaml:"https_proxy"`
	// Theme selects the TUI palette: "auto" (default, follow the terminal),
	// "light", "dark", "high-contrast", "ocean", or "matrix". Named themes
	// (everything except auto) also set a matching terminal background so
	// adaptive text remains readable.
	Theme string `yaml:"theme"`
	// Verbose enables detailed logging to a file.
	Verbose bool `yaml:"verbose"`
	// LogFile is the path where verbose output is written.
	LogFile string `yaml:"log_file"`
	// LogLevel controls logging verbosity: silent, info, or debug.
	LogLevel string `yaml:"log_level"`
}

// Default returns a Config populated with sensible defaults.
func Default() Config {
	return Config{
		BundleName:     "images",
		Compression:    "gzip",
		Concurrency:    4,
		HTTPSProxy:     "",
		ImagesFile:     "images.yaml",
		LogLevel:       "info",
		MinFreeDiskMB:  2048,
		OutputDir:      "archives",
		Platform:       "linux/amd64",
		RegistryPrefix: "",
		Retries:        2,
		TempDir:        os.TempDir(),
		Theme:          ThemeAuto,
	}
}

// ValidateTheme reports whether name is a supported TUI theme.
func ValidateTheme(name string) error {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", ThemeAuto, ThemeLight, ThemeDark, ThemeHighContrast, ThemeOcean, ThemeMatrix:
		return nil
	default:
		return fmt.Errorf("unsupported theme %q (want auto, light, dark, high-contrast, ocean, or matrix)", name)
	}
}

// NormalizeTheme returns a canonical theme name. Empty becomes auto.
func NormalizeTheme(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return ThemeAuto
	}
	return n
}

// ValidateProxy reports whether rawURL is a usable proxy URL. An empty value is
// valid (no proxy). It runs the same url.Parse the registry puller would
// otherwise run lazily on the first pull, so a malformed proxy fails once at
// startup instead of once per image after the download path has begun. The
// puller keeps its lazy parse as a second line of defence.
func ValidateProxy(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid proxy URL %q: %w", rawURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid proxy URL %q: missing scheme or host", rawURL)
	}
	return nil
}

// ThemeMenuIndex returns the index of name in ThemeMenu, or 0 (auto) if unknown.
func ThemeMenuIndex(name string) int {
	n := NormalizeTheme(name)
	for i, t := range ThemeMenu {
		if t == n {
			return i
		}
	}
	return 0
}

// ThemeIsForced reports whether name paints a fixed palette (not auto).
func ThemeIsForced(name string) bool {
	return NormalizeTheme(name) != ThemeAuto
}

// LoadRequired is Load, but a missing file is an error. Use it when the user
// explicitly named a config path (e.g. -config /path): silently falling back to
// defaults would hide a typo'd path from an unattended run.
func LoadRequired(path string) (Config, error) {
	if _, err := os.Stat(path); err != nil {
		return Default(), fmt.Errorf("config file %q: %w", path, err)
	}
	return Load(path)
}

// Load reads configuration from path, falling back to defaults for any
// unset field. A missing file is not an error: defaults are returned. Unknown
// keys are rejected, matching the image list: a mistyped field name would
// otherwise be silently ignored and the run would use a default the user never
// intended.
func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return cfg, err
	}
	return cfg, nil
}

// DefaultPath returns the config file path to load when -config is not set.
//
// Preference:
//  1. An existing file among the candidates (so either convention works).
//  2. Otherwise the primary create path: $XDG_CONFIG_HOME if set, else
//     ~/.config/dockerdownloader/config.yaml (matches docs and common CLI
//     tooling). os.UserConfigDir is only a create fallback when home is
//     unknown — on macOS that is ~/Library/Application Support, which is
//     not where users expect a terminal tool config.
//
// Candidates checked for existence (in order):
//   - $XDG_CONFIG_HOME/dockerdownloader/config.yaml (when XDG_CONFIG_HOME is set)
//   - ~/.config/dockerdownloader/config.yaml
//   - $UserConfigDir/dockerdownloader/config.yaml
func DefaultPath() string {
	paths := candidateConfigPaths()
	for _, p := range paths {
		if fileExists(p) {
			return p
		}
	}
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}

// candidateConfigPaths returns unique candidate config paths in priority order.
func candidateConfigPaths() []string {
	out := make([]string, 0, 3)
	seen := make(map[string]struct{}, 3)
	add := func(p string) {
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		add(filepath.Join(xdg, "dockerdownloader", "config.yaml"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".config", "dockerdownloader", "config.yaml"))
	}
	if dir, err := os.UserConfigDir(); err == nil {
		add(filepath.Join(dir, "dockerdownloader", "config.yaml"))
	}
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// FindWritableTempDir returns a writable temporary directory, starting with
// preferred. If preferred is empty, the system temporary directory is tried.
// If the requested directory is not usable, it falls back to common
// alternatives and returns a warning to show the user. If no candidate is
// usable, it returns an error describing why the preferred one failed.
func FindWritableTempDir(preferred string) (dir, warn string, err error) {
	sysTemp := os.TempDir()
	if preferred == "" {
		preferred = sysTemp
	}
	candidates := []string{preferred, sysTemp}
	candidates = append(candidates, fallbackTempDirs()...)
	if cwd, _ := os.Getwd(); cwd != "" && cwd != preferred && cwd != sysTemp {
		candidates = append(candidates, cwd)
	}
	seen := make(map[string]struct{}, len(candidates))
	uniq := make([]string, 0, len(candidates))
	for _, d := range candidates {
		if d == "" {
			continue
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		uniq = append(uniq, d)
	}
	candidates = uniq

	preferredClean := filepath.Clean(preferred)
	var preferredErr error
	for _, d := range candidates {
		clean := filepath.Clean(d)
		if cerr := EnsureWritableDir(clean); cerr != nil {
			if clean == preferredClean {
				preferredErr = cerr
			}
			continue
		}
		if clean == preferredClean {
			return clean, "", nil
		}
		if preferredErr != nil {
			return clean, fmt.Sprintf("warning: temp dir %s is not usable (%v), using fallback %s", preferred, preferredErr, clean), nil
		}
		return clean, "", nil
	}
	if preferredErr != nil {
		return "", "", fmt.Errorf("no writable temporary directory found (preferred %s: %w)", preferred, preferredErr)
	}
	return "", "", fmt.Errorf("no writable temporary directory found (tried: %s)", strings.Join(candidates, ", "))
}

// EnsureWritableDir checks that dir exists and can be written to. If dir does
// not exist, it attempts to create it. Any write-test file is removed before
// returning.
func EnsureWritableDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("directory check: %w", err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}
	} else if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	f, err := os.CreateTemp(dir, "dockerdownloader-writetest-*")
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("%s is not writable", dir)
		}
		return fmt.Errorf("write test: %w", err)
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return nil
}

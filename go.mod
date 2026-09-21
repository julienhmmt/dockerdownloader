module github.com/julienhmmt/dockerdownloader

go 1.27.0

// go1.27.1 is the patched stable toolchain. CI resolves this directive via
// actions/setup-go's go-version-file and runs with GOTOOLCHAIN=local, so this
// line is what decides which stdlib govulncheck inspects. With
// GOTOOLCHAIN=auto, the go command downloads this toolchain when the host is
// older.
//
// Keep in sync with the go directive: raising "go" to a language version newer
// than the Go used to build golangci-lint makes lint fail outright
// ("the Go language version ... is lower than the targeted Go version"), so
// golangci-lint in CI must be v2.13.2+ (built with go1.27).
toolchain go1.27.1

require (
	charm.land/bubbles/v2 v2.2.1
	charm.land/bubbletea/v2 v2.0.9
	charm.land/lipgloss/v2 v2.0.6
	github.com/google/go-containerregistry v0.22.1
	github.com/klauspost/compress v1.20.0
	github.com/stretchr/testify v1.12.1
	golang.org/x/sync v0.23.0
	golang.org/x/sys v0.48.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/harmonica v0.2.0 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20260910203606-6c9e17dc7a16 // indirect
	github.com/charmbracelet/x/ansi v0.11.8 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/docker/cli v29.8.1+incompatible // indirect
	github.com/docker/docker-credential-helpers v0.9.9 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-runewidth v0.0.30 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/sirupsen/logrus v1.10.2 // indirect
	github.com/xo/terminfo v1.2.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	gotest.tools/v3 v3.5.2 // indirect
)

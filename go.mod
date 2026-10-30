module github.com/julienhmmt/dockerdownloader

// go1.27.3 is the minimum: earlier 1.27.x patch releases ship the crypto/x509
// flaw GO-2026-6809, reachable through the TLS registry pulls. The go directive
// (not a separate toolchain line) enforces it, so neither a local build nor
// CI (actions/setup-go reads go-version-file) can compile with an older
// stdlib. With GOTOOLCHAIN=auto, the go command downloads go1.27.3 when the
// host is older.
//
// golangci-lint refuses a module whose go version is newer than the Go it
// was built with, so CI builds it from source with this toolchain.
go 1.27.3

require (
	charm.land/bubbles/v2 v2.2.1
	charm.land/bubbletea/v2 v2.1.1
	charm.land/lipgloss/v2 v2.0.6
	github.com/google/go-containerregistry v0.22.1
	github.com/klauspost/compress v1.20.3
	github.com/stretchr/testify v1.12.1
	golang.org/x/sync v0.24.0
	golang.org/x/sys v0.49.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/harmonica v0.2.0 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20261023060817-a45b7c86333f // indirect
	github.com/charmbracelet/x/ansi v0.11.9 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/docker/cli v29.9.2+incompatible // indirect
	github.com/docker/docker-credential-helpers v0.9.12 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-runewidth v0.0.31 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/sirupsen/logrus v1.10.2 // indirect
	github.com/xo/terminfo v1.3.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	gotest.tools/v3 v3.5.2 // indirect
)

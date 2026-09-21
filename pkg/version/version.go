// Package version holds build-time identity for dockerdownloader.
package version

// Version is the release version. Overridden at link time via
// -ldflags "-X github.com/julienhmmt/dockerdownloader/pkg/version.Version=...".
// Default "dev" marks non-release builds.
var Version = "dev"

// String returns the tool identity, e.g. "dockerdownloader 0.4.0" or
// "dockerdownloader dev".
func String() string {
	return "dockerdownloader " + Version
}

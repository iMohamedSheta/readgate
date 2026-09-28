// Package version carries the release tag baked in at build time:
//
//	wails build -ldflags "-X readgate/internal/version.Version=v1.2.3"
//
// Local builds report "dev".
package version

// Version is overwritten by release builds via -ldflags.
var Version = "dev"

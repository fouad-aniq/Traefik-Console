// Package version holds the Console's release version, set at build time:
//
//	go build -ldflags "-X github.com/traefik-console/internal/version.Version=0.1.0"
package version

// Version is the release this binary was built from ("dev" for local builds).
var Version = "dev"

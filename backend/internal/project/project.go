// Package project holds the values a maintainer changes when the project moves or a dependency
// releases: where releases are published, the CrowdSec plugin Console installs, the external
// services Console talks to, and how Console names itself to them.
//
// Values that are stored identifiers (cookie names, CrowdSec registration prefixes, managed file
// names, encryption labels) are deliberately not here: changing one breaks existing installs.
package project

import (
	"time"

	"github.com/traefik-console/internal/version"
)

// Releases.
const (
	// Repository is the GitHub repository ("owner/name") where Console releases are published.
	Repository = "fouad-aniq/Traefik-Console"
	// UpdateCheckInterval is how long an answer from GitHub's release list is reused.
	UpdateCheckInterval = 5 * time.Minute
)

// ReleasesAPIURL lists the newest releases, pre-releases included.
func ReleasesAPIURL() string {
	return "https://api.github.com/repos/" + Repository + "/releases?per_page=30"
}

// ReleasePageURL is the release notes page of one tag.
func ReleasePageURL(tag string) string {
	return "https://github.com/" + Repository + "/releases/tag/" + tag
}

// CrowdSec bouncer plugin that Console adds to Traefik's static configuration.
const (
	CrowdSecPluginModule = "github.com/maxlerebourg/crowdsec-bouncer-traefik-plugin"
	// CrowdSecPluginVersion is the version a new integration starts with. Keep
	// THIRD-PARTY-NOTICES.md in step when changing it.
	CrowdSecPluginVersion = "v1.8.2"
)

// External services.
const (
	// PluginCatalogURL is the Traefik plugin catalog API.
	PluginCatalogURL = "https://plugins.traefik.io/api/services/plugins"
	// Let's Encrypt ACME directories offered by the resolver form.
	LetsEncryptProductionURL = "https://acme-v02.api.letsencrypt.org/directory"
	LetsEncryptStagingURL    = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// Name is how Console identifies itself in User-Agent headers.
const Name = "Traefik-Console"

// UserAgent is the User-Agent Console sends to GitHub, CrowdSec and other services, with the
// version of the running build, e.g. "Traefik-Console/0.1.0-rc.15".
func UserAgent() string {
	return Name + "/" + version.Version
}

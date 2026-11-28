package project

import (
	"testing"

	"github.com/traefik-console/internal/version"
)

func TestUserAgentCarriesTheRunningVersion(t *testing.T) {
	defer func(previous string) { version.Version = previous }(version.Version)
	version.Version = "0.1.0-rc.15"
	if got := UserAgent(); got != "Traefik-Console/0.1.0-rc.15" {
		t.Fatal(got)
	}
}

func TestReleaseURLsUseTheRepository(t *testing.T) {
	if got := ReleasesAPIURL(); got != "https://api.github.com/repos/"+Repository+"/releases?per_page=30" {
		t.Fatal(got)
	}
	if got := ReleasePageURL("v1.2.3"); got != "https://github.com/"+Repository+"/releases/tag/v1.2.3" {
		t.Fatal(got)
	}
}

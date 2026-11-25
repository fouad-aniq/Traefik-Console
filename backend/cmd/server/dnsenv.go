package main

import (
	"strings"

	"github.com/traefik-console/internal/config"
	"github.com/traefik-console/internal/core/resolvers/providers"
)

// ensureDNSEnvFile declares every supported DNS provider variable in Traefik's env file (created
// when missing), so a Traefik that loads it never needs new variables: resolvers only add secret
// files. A failure is logged; the credential check then reports what Traefik is missing.
func ensureDNSEnvFile(opts *config.Options) {
	if opts.DNSEnv == nil {
		return
	}
	registry, err := providers.LoadRegistry()
	if err != nil {
		diagLog.Errorf("dns provider env file: %v", err)
		return
	}
	want := map[string]string{}
	for _, env := range registry.EnvVars() {
		want[env+"_FILE"] = opts.DNSEnv.SecretsRoot + "/" + strings.ToLower(env) + ".secret"
	}
	if _, err = opts.DNSEnv.Ensure(want); err != nil {
		diagLog.Errorf("dns provider env file %s: %v", opts.DNSEnv.Path, err)
	}
}

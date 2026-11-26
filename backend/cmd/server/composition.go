package main

import (
	"fmt"
	"strings"

	api "github.com/traefik-console/internal/api/handlers"
	"github.com/traefik-console/internal/config"
	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/config/secrets"
	"github.com/traefik-console/internal/core/certificates"
	"github.com/traefik-console/internal/core/crowdsec"
	"github.com/traefik-console/internal/core/domains"
	"github.com/traefik-console/internal/core/entrypoints"
	"github.com/traefik-console/internal/core/middlewares"
	"github.com/traefik-console/internal/core/plugins"
	"github.com/traefik-console/internal/core/resolvers"
	"github.com/traefik-console/internal/core/routers"
	"github.com/traefik-console/internal/core/services"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/docker"
	"github.com/traefik-console/internal/engine"
	catalog "github.com/traefik-console/internal/traefik/plugins"
	"github.com/traefik-console/internal/traefik/status"
	"github.com/traefik-console/internal/updates"
	"github.com/traefik-console/internal/version"
)

// Startup constructs resource use cases directly into the existing API dependency value.
// No aggregate core runtime, getters, global registry or file-writing API capability is introduced.
func newAPIDependencies(opts *config.Options, store db.Store, filesMgr *files.Repository,
	secretsMgr *secrets.Vault, inspector *status.ObservationClient, runtimeClient docker.Operations,
	operations engine.Operations, catalogClient *catalog.CatalogClient,
) (api.Dependencies, error) {
	if operations == nil {
		return api.Dependencies{}, fmt.Errorf("HTTP composition: operation engine is required")
	}
	resolverManager, err := resolvers.NewResolverManager(store, filesMgr, secretsMgr, operations, opts.RuntimeACMEDir)
	if err != nil {
		return api.Dependencies{}, fmt.Errorf("HTTP composition: resolvers: %w", err)
	}
	crowdsecManager, err := crowdsec.NewCrowdSecManagerWithOptions(store, secretsMgr, runtimeClient, opts.CrowdSec.LAPIURL, integrationSettings(opts), operations, opts.ProtectedCIDRs)
	if err != nil {
		return api.Dependencies{}, fmt.Errorf("HTTP composition: crowdsec: %w", err)
	}
	observer := status.NewACMEObserver(opts.ACMEObservationDir, opts.RuntimeACMEDir)
	return api.Dependencies{
		History: store, Observation: inspector, Container: runtimeClient, Operations: operations,
		ReadStaticConfig: filesMgr.ReadStaticConfig,
		Resolvers:        resolverManager,
		EntryPoints:      entrypoints.NewEntryPointManager(store, filesMgr, operations),
		Domains:          domains.NewDomainManager(store, filesMgr, opts.ProbeTargets, operations, observer),
		Certificates:     certificates.NewCertificateManager(store, secretsMgr, operations),
		Middlewares:      middlewares.NewMiddlewareManager(store, secretsMgr, inspector, operations),
		Services:         services.NewServiceManager(store, inspector, operations),
		Routers:          routers.NewRouterManager(store, filesMgr, inspector, operations),
		Plugins:          plugins.NewManager(store, filesMgr, catalogClient, operations),
		CrowdSec:         crowdsecManager, ACMEObserver: observer,
		Updates: updates.New(version.Version, opts.UpdateCheck),
	}, nil
}

func integrationSettings(opts *config.Options) crowdsec.Settings {
	bouncerCert, bouncerKey := opts.CrowdSec.BouncerCertFile, opts.CrowdSec.BouncerKeyFile
	machineCert, machineKey := opts.CrowdSec.MachineCertFile, opts.CrowdSec.MachineKeyFile
	if opts.CrowdSec.ClientCertFile != "" || opts.CrowdSec.ClientKeyFile != "" {
		bouncerCert, bouncerKey = opts.CrowdSec.ClientCertFile, opts.CrowdSec.ClientKeyFile
		machineCert, machineKey = opts.CrowdSec.ClientCertFile, opts.CrowdSec.ClientKeyFile
	}
	return crowdsec.Settings{
		APIKey: opts.CrowdSec.APIKey, MachineID: opts.CrowdSec.MachineID, MachinePassword: opts.CrowdSec.MachinePassword,
		CAFile: opts.CrowdSec.CAFile, BouncerCertFile: bouncerCert, BouncerKeyFile: bouncerKey,
		MachineCertFile: machineCert, MachineKeyFile: machineKey,
		AppSecURL: opts.CrowdSec.AppSecURL, MetricsURL: opts.CrowdSec.MetricsURL, MetricsToken: opts.CrowdSec.MetricsToken, AccessLogSource: opts.CrowdSec.AccessLogSource,
		ReadTimeout: opts.CrowdSec.ReadTimeout, AlertLimit: opts.CrowdSec.AlertLimit,
		ReadTimeoutSet: opts.CrowdSec.ReadTimeoutSet, AlertLimitSet: opts.CrowdSec.AlertLimitSet,
		ProtectedCIDRsSet: opts.CrowdSec.ProtectedCIDRsSet, Environment: crowdsecEnvironment(opts),
		Runtime:           crowdsec.RuntimeRegistration{DockerConfigured: opts.Docker.Host != "", ContainerID: opts.Docker.CrowdSecContainer},
		RuntimeSecretsDir: opts.RuntimeSecretsDir,
	}
}

// crowdsecEnvironment reports which CROWDSEC_* variables were set, in a stable order, without secret values.
func crowdsecEnvironment(opts *config.Options) []crowdsec.EnvVariable {
	out := []crowdsec.EnvVariable{}
	for _, name := range config.CrowdSecEnvironmentVariables {
		raw, ok := opts.CrowdSec.Environment[name]
		if !ok {
			continue
		}
		field := crowdsec.EnvironmentField[name]
		variable := crowdsec.EnvVariable{Name: name, Field: field.Field, Secret: field.Secret}
		if !field.Secret {
			variable.Value = strings.TrimSpace(raw)
		}
		out = append(out, variable)
	}
	return out
}

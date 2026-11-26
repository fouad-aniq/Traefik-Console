package crowdsec

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/traefik-console/internal/docker"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/validation"
)

// Inventory is what the caller already knows about the rest of the configuration; the manager owns no
// plugin, middleware or router listing of its own (external declarations are only visible to those managers).
type Inventory struct {
	Plugins     []models.UIResource[models.StaticPluginConfig]
	Middlewares []models.UIResource[models.MiddlewareConfig]
	Routers     []models.UIResource[models.RouterConfig]
}

// PluginStatus is the bouncer plugin declaration. State is missing | installed.
type PluginStatus struct {
	State      string `json:"state"`
	Alias      string `json:"alias,omitempty"`
	ModuleName string `json:"module_name,omitempty"`
	Version    string `json:"version,omitempty"`
	Origin     string `json:"origin,omitempty"`
}

// MiddlewareStatus is the bouncer middleware. State uses the same vocabulary as PluginStatus.
type MiddlewareStatus struct {
	State       string `json:"state"`
	Name        string `json:"name,omitempty"`
	PluginAlias string `json:"plugin_alias,omitempty"`
	Origin      string `json:"origin,omitempty"`
}

type KeyStatus struct {
	Present bool `json:"present"`
	// File is the bouncer key file as Traefik sees it, when the Console stores the key: a bouncer
	// configured outside the Console reads it with crowdsecLapiKeyFile.
	File string `json:"file,omitempty"`
}

type MachineStatus struct {
	State string `json:"state"` // not_configured | ok | invalid_key | unreachable
	ID    string `json:"id,omitempty"`
}

type ObservationStatus struct {
	State string `json:"state"`
}

type CrowdSecCapabilities struct {
	AutoEnroll     bool `json:"auto_enroll"`
	Alerts         bool `json:"alerts"`
	Decisions      bool `json:"decisions"`
	DecisionCreate bool `json:"decision_create"`
	DecisionDelete bool `json:"decision_delete"`
	AllowlistRead  bool `json:"allowlist_read"`
	AllowlistWrite bool `json:"allowlist_write"`
	AppSec         bool `json:"appsec"`
	Metrics        bool `json:"metrics"`
	HubRead        bool `json:"hub_read"`
}

// LAPIStatus State is ok | invalid_key | unreachable | unchecked (no key yet or protection disabled).
type LAPIStatus struct {
	State string `json:"state"`
	URL   string `json:"url"`
}

// EngineStatus State is not_configured | stopped | running | unavailable.
type EngineStatus struct {
	State string `json:"state"`
	Name  string `json:"name,omitempty"`
	Image string `json:"image,omitempty"`
}

type RouterProtection struct {
	Name      string `json:"name"`
	Origin    string `json:"origin"`
	Protected bool   `json:"protected"`
}

// CrowdSecStatus describes every part of the integration separately, so a caller can add exactly what is
// missing. Configured is not protected: nothing here proves live blocking.
type CrowdSecSources struct {
	LAPIURL               string `json:"lapi_url"`
	BouncerAuthentication string `json:"bouncer_authentication"`
	Machine               string `json:"machine"`
	CACertificate         string `json:"ca_certificate"`
	ClientCertificate     string `json:"client_certificate"`
	ReadTimeout           string `json:"read_timeout"`
	AlertLimit            string `json:"alert_limit"`
	AppSecURL             string `json:"appsec_url"`
	MetricsURL            string `json:"metrics_url"`
	MetricsToken          string `json:"metrics_token"`
	AccessLogSource       string `json:"access_log_source"`
	ProtectedCIDRs        string `json:"protected_cidrs"`
}

type CrowdSecStatus struct {
	Engine       EngineStatus         `json:"engine"`
	Plugin       PluginStatus         `json:"plugin"`
	Middleware   MiddlewareStatus     `json:"middleware"`
	Key          KeyStatus            `json:"key"`
	LAPI         LAPIStatus           `json:"lapi"`
	Machine      MachineStatus        `json:"machine"`
	Ingestion    ObservationStatus    `json:"ingestion"`
	Remediation  ObservationStatus    `json:"remediation"`
	Capabilities CrowdSecCapabilities `json:"capabilities"`
	Sources      CrowdSecSources      `json:"sources"`
	ObservedAt   string               `json:"observed_at"`
	Metrics      *MetricsSnapshot     `json:"metrics,omitempty"`
	Bouncers     []docker.Bouncer     `json:"bouncers"`
	Routers      []RouterProtection   `json:"routers"`
	Config       CrowdSecConfig       `json:"config"`
	// Environment lists the CROWDSEC_* variables set at startup; their settings are read-only in the Console.
	Environment []EnvVariable       `json:"environment"`
	Runtime     RuntimeRegistration `json:"runtime"`
	// Complete is true when plugin, middleware and key exist and the LAPI accepts the key.
	Complete bool `json:"complete"`
}

// findPlugin picks the declaration of the bouncer module: the default alias first, then managed before
// external, then by name.
func findPlugin(plugins []models.UIResource[models.StaticPluginConfig]) (models.UIResource[models.StaticPluginConfig], bool) {
	want := validation.PluginModuleKey(crowdsecPluginModule)
	var best models.UIResource[models.StaticPluginConfig]
	found := false
	rank := func(p models.UIResource[models.StaticPluginConfig]) int {
		score := 0
		if p.Name != crowdsecPluginShortName {
			score += 2
		}
		if p.Origin == "external" {
			score++
		}
		return score
	}
	for _, p := range plugins {
		if validation.PluginModuleKey(p.Config.ModuleName) != want {
			continue
		}
		if !found || rank(p) < rank(best) || rank(p) == rank(best) && p.Name < best.Name {
			best, found = p, true
		}
	}
	return best, found
}

func hasMiddleware(names []string, name string) bool {
	for _, n := range names {
		if n == name || n == name+"@file" {
			return true
		}
	}
	return false
}

func settingSource(environment, saved bool) string {
	if environment {
		return "environment"
	}
	if saved {
		return "saved"
	}
	return "none"
}

// Status reads the current state of every integration part. Credential probes are independent of managed protection.
func (m *crowdsecManager) Status(ctx context.Context, inv Inventory) (*CrowdSecStatus, error) {
	cfg, err := m.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	out := &CrowdSecStatus{Config: *cfg, Bouncers: []docker.Bouncer{}, Routers: []RouterProtection{}, LAPI: LAPIStatus{State: "unchecked", URL: cfg.LAPIURL}, Machine: MachineStatus{State: "not_configured"}, Ingestion: ObservationStatus{State: "unverified"}, ObservedAt: time.Now().UTC().Format(time.RFC3339)}
	out.Remediation.State = "unverified"
	if !cfg.Enabled && cfg.IntegrationMode != "external" {
		out.Remediation.State = "disabled"
	}
	out.Config.LAPIKey = ""
	out.Config.MachinePassword = ""
	out.Plugin = PluginStatus{State: "missing"}
	if p, ok := findPlugin(inv.Plugins); ok {
		out.Plugin = PluginStatus{State: "installed", Alias: p.Name, ModuleName: p.Config.ModuleName, Version: p.Config.Version, Origin: p.Origin}
	}

	mwName := cfg.MiddlewareName
	if mwName == "" {
		mwName = crowdsecPluginShortName
	}
	out.Middleware = MiddlewareStatus{State: "missing"}
	var byPlugin *models.UIResource[models.MiddlewareConfig]
	for i, mw := range inv.Middlewares {
		if mw.Name == mwName {
			out.Middleware = MiddlewareStatus{State: "installed", Name: mw.Name, PluginAlias: mw.Config.PluginName, Origin: mw.Origin}
			byPlugin = nil
			break
		}
		if byPlugin == nil && out.Plugin.Alias != "" && mw.Config.Type == "plugin" && mw.Config.PluginName == out.Plugin.Alias {
			byPlugin = &inv.Middlewares[i]
		}
	}
	if out.Middleware.State == "missing" && byPlugin != nil {
		out.Middleware = MiddlewareStatus{State: "installed", Name: byPlugin.Name, PluginAlias: byPlugin.Config.PluginName, Origin: byPlugin.Origin}
	}

	savedCA, err := m.secretPEM(ctx, crowdsecCACertSecret)
	if err != nil {
		return nil, err
	}
	savedCert, err := m.secretPEM(ctx, crowdsecClientCertName)
	if err != nil {
		return nil, err
	}
	savedKey, err := m.secretPEM(ctx, crowdsecClientKeySecret)
	if err != nil {
		return nil, err
	}
	persisted := cfg.Version > 0
	out.Sources = CrowdSecSources{
		LAPIURL:               settingSource(m.defaultLAPIURL != "", persisted && m.defaultLAPIURL == "" && cfg.LAPIURL != ""),
		BouncerAuthentication: settingSource(m.settings.APIKey != "", m.settings.APIKey == "" && m.bouncerCredential(ctx) != ""),
		Machine:               settingSource(m.settings.MachineID != "" || m.settings.MachinePassword != "", persisted && cfg.MachineID != ""),
		CACertificate:         settingSource(m.settings.CAFile != "", m.settings.CAFile == "" && len(savedCA) > 0),
		ClientCertificate:     settingSource(m.environmentClientCertificate(false) || m.environmentClientCertificate(true), len(savedCert) > 0 && len(savedKey) > 0),
		ReadTimeout:           settingSource(m.settings.ReadTimeoutSet, persisted && !m.settings.ReadTimeoutSet),
		AlertLimit:            settingSource(m.settings.AlertLimitSet, persisted && !m.settings.AlertLimitSet),
		AppSecURL:             settingSource(m.settings.AppSecURL != "", cfg.AppSecURL != ""),
		MetricsURL:            settingSource(m.settings.MetricsURL != "", cfg.MetricsURL != ""),
		MetricsToken:          settingSource(m.settings.MetricsToken != "", cfg.MetricsTokenSet),
		AccessLogSource:       settingSource(m.settings.AccessLogSource != "", cfg.AccessLogSource != ""),
		ProtectedCIDRs:        settingSource(m.settings.ProtectedCIDRsSet, persisted && !m.settings.ProtectedCIDRsSet && m.savedProtectedCIDRs(ctx)),
	}
	out.Environment = append([]EnvVariable{}, m.settings.Environment...)
	out.Runtime = m.settings.Runtime

	key := m.bouncerCredential(ctx)
	out.Key.Present = key != ""
	// The key file exists only for a key the Console stores (not one given by CROWDSEC_API_KEY).
	if key != "" && strings.TrimSpace(m.settings.APIKey) == "" {
		if row, e := m.store.GetSecret(ctx, crowdsecSecretName); e == nil && row != nil {
			out.Key.File = row.FilePath
		}
	}
	bouncerCertificate, err := m.hasClientCertificate(ctx, false)
	if err != nil {
		return nil, err
	}
	if (out.Key.Present || bouncerCertificate) && cfg.LAPIURL != "" {
		verifyKey := key
		if bouncerCertificate {
			verifyKey = ""
		}
		verifyCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		switch verr := m.verifyLAPIConnection(verifyCtx, cfg.LAPIURL, verifyKey); {
		case verr == nil:
			out.LAPI.State = "ok"
		case errors.Is(verr, ErrCrowdSecInvalidKey):
			out.LAPI.State = "invalid_key"
		default:
			out.LAPI.State = "unreachable"
		}
		cancel()
	}

	machineID, machinePassword, machinePasswordPresent := m.machineCredentials(ctx)
	machineCertificate, err := m.hasClientCertificate(ctx, true)
	if err != nil {
		return nil, err
	}
	machinePresent := machinePasswordPresent || machineCertificate
	if machinePresent && cfg.LAPIURL != "" {
		out.Machine.ID = machineID
		verifyCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		switch verr := m.verifyMachineCredentials(verifyCtx, machineID, machinePassword); {
		case verr == nil:
			out.Machine.State = "ok"
		case errors.Is(verr, ErrCrowdSecInvalidKey):
			out.Machine.State = "invalid_key"
		default:
			out.Machine.State = "unreachable"
		}
		cancel()
	}

	out.Engine = EngineStatus{State: "unavailable"}
	if inspector, ok := m.decisions.(docker.CrowdSecInspector); ok {
		if engine, e := inspector.CrowdSecEngine(ctx); e == nil {
			switch {
			case !engine.Configured:
				out.Engine.State = "not_configured"
			case engine.Running:
				out.Engine = EngineStatus{State: "running", Name: engine.Name, Image: engine.Image}
				if bouncers, e := inspector.ListBouncers(ctx); e == nil {
					out.Bouncers = bouncers
				}
			default:
				out.Engine = EngineStatus{State: "stopped", Name: engine.Name, Image: engine.Image}
			}
		}
	}

	engineRunning := out.Engine.State == "running"
	out.Runtime.Accessible = engineRunning
	bouncerIdentity := out.Key.Present || bouncerCertificate
	_, bouncerEnrollment := m.decisions.(docker.BouncerRegistrar)
	_, machineEnrollment := m.decisions.(docker.MachineRegistrar)
	_, allowlistContainer := m.decisions.(docker.AllowlistManager)
	_, hubContainer := m.decisions.(docker.HubManager)
	out.Capabilities = CrowdSecCapabilities{
		AutoEnroll:     engineRunning && bouncerEnrollment && machineEnrollment,
		Alerts:         machinePresent || engineRunning,
		Decisions:      bouncerIdentity || machinePresent || engineRunning,
		DecisionCreate: engineRunning,
		DecisionDelete: machinePresent || engineRunning,
		AllowlistRead:  machinePresent || engineRunning && allowlistContainer,
		AllowlistWrite: engineRunning && allowlistContainer,
		AppSec:         cfg.AppSecURL != "",
		Metrics:        cfg.MetricsURL != "",
		HubRead:        engineRunning && hubContainer,
	}

	attached := out.Middleware.Name
	if attached == "" {
		attached = mwName
	}
	for _, r := range inv.Routers {
		if models.RouterProtocol(r.Config) != models.ServiceHTTP {
			continue // the bouncer is an HTTP middleware: TCP and UDP routers cannot carry it
		}
		out.Routers = append(out.Routers, RouterProtection{Name: r.Name, Origin: r.Origin, Protected: hasMiddleware(r.Config.Middlewares, attached)})
	}
	if cfg.MetricsURL != "" {
		out.Metrics = m.Metrics(ctx)
		out.Ingestion.State = out.Metrics.IngestionState
	}
	if cfg.IntegrationMode == "external" {
		out.Complete = cfg.LAPIURL != "" && out.LAPI.State == "ok" && (bouncerIdentity || machinePresent)
	} else {
		out.Complete = cfg.LAPIURL != "" && out.Plugin.State != "missing" && out.Middleware.State != "missing" && out.Key.Present && out.LAPI.State == "ok"
	}
	return out, nil
}

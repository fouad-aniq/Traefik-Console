package crowdsec

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/traefik-console/internal/docker"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/project"
)

const (
	crowdsecPluginShortName = "crowdsec-bouncer"
	crowdsecPluginModule    = project.CrowdSecPluginModule
	crowdsecDefaultVersion  = project.CrowdSecPluginVersion
	crowdsecSecretName      = "crowdsec_lapi_key"
	crowdsecMachineSecret   = "crowdsec_machine_password"
	crowdsecCACertSecret    = "crowdsec_ca_certificate"
	crowdsecClientCertName  = "crowdsec_client_certificate"
	crowdsecClientKeySecret = "crowdsec_client_key"
	managerSecretPurpose    = "crowdsec_manager"
)

type CrowdSecConfig struct {
	// Version is the committed row version (optimistic concurrency); never stored in the payload.
	Version                int64    `json:"version"`
	IntegrationMode        string   `json:"integration_mode,omitempty"` // "managed" or "external"; defaults to managed for existing rows.
	ReadTimeoutSeconds     int      `json:"read_timeout_seconds"`
	AlertLimit             int      `json:"alert_limit"`
	CACertificate          string   `json:"ca_certificate,omitempty"`
	ClientCertificate      string   `json:"client_certificate,omitempty"`
	ClientKey              string   `json:"client_key,omitempty"`
	ActiveEnabled          bool     `json:"active_enabled"`
	State                  string   `json:"state,omitempty"`
	Routers                []string `json:"routers,omitempty"`
	TrustedProxyCIDRs      []string `json:"trusted_proxy_cidrs,omitempty"`
	EnrollmentIDs          []int64  `json:"enrollment_ids,omitempty"`
	MachineID              string   `json:"machine_id,omitempty"`
	MachinePassword        string   `json:"machine_password,omitempty"` // Raw on write, masked on read.
	AutoEnrollMachine      bool     `json:"auto_enroll_machine,omitempty"`
	MachineEnrollmentID    int64    `json:"machine_enrollment_id,omitempty"`
	UpdateMaxFailure       int      `json:"update_max_failure"` // -1 fails open; 0 blocks on the first update failure.
	HTTPTimeoutSeconds     int      `json:"http_timeout_seconds"`
	AppSecEnabled          bool     `json:"appsec_enabled"`
	AppSecURL              string   `json:"appsec_url,omitempty"`
	AppSecFailureBlock     *bool    `json:"appsec_failure_block,omitempty"`
	AppSecUnreachableBlock *bool    `json:"appsec_unreachable_block,omitempty"`
	AppSecBodyLimit        int64    `json:"appsec_body_limit"`
	ClientTrustedCIDRs     []string `json:"client_trusted_cidrs,omitempty"`
	Enabled                bool     `json:"enabled"`
	Mode                   string   `json:"mode"`           // "live", "stream", "alone"
	LAPIURL                string   `json:"lapi_url"`       // e.g. "http://crowdsec:8080"
	LAPIKey                string   `json:"lapi_key"`       // Raw key on POST, masked preview on GET
	AutoEnroll             bool     `json:"auto_enroll"`    // If true, auto-generates key via docker exec cscli bouncers add
	PluginVersion          string   `json:"plugin_version"` // Default: "v1.3.5"
	// PluginAlias reuses an existing bouncer plugin declaration (managed or external) instead of staging one.
	PluginAlias string `json:"plugin_alias,omitempty"`
	// MiddlewareName is the bouncer middleware routers get attached to; default "crowdsec-bouncer".
	MiddlewareName  string `json:"middleware_name,omitempty"`
	MiddlewareOwned bool   `json:"middleware_owned"`
	// Connection settings saved by the Console when the matching CROWDSEC_* variable is not set.
	MetricsURL string `json:"metrics_url,omitempty"`
	// MetricsToken is write-only; MetricsTokenSet reports whether one is saved or set by the environment.
	MetricsToken    string   `json:"metrics_token,omitempty"`
	MetricsTokenSet bool     `json:"metrics_token_set,omitempty"`
	AccessLogSource string   `json:"access_log_source,omitempty"`
	ProtectedCIDRs  []string `json:"protected_cidrs,omitempty"`
	// KeepEnrollments marks a save of an integration that was already disabled: Console-registered keys
	// stay registered (they are used to read decisions). Only disabling enabled protection revokes them.
	KeepEnrollments bool `json:"keep_enrollments,omitempty"`
}

type CrowdSecDecision = docker.Decision

type CrowdSecManager interface {
	VerifyKey(context.Context, string) error
	GetConfig(ctx context.Context) (*CrowdSecConfig, error)
	ConfigureBouncer(ctx context.Context, input CrowdSecConfig, opts models.WriteOptions) (*models.WriteResult[CrowdSecConfig], error)
	ListDecisions(ctx context.Context) ([]CrowdSecDecision, error)
	AddDecision(ctx context.Context, ipOrCIDR, duration, reason string) error
	DeleteDecision(ctx context.Context, ipOrCIDR string) error
	DeleteDecisionByID(ctx context.Context, id int64) error
	ListAlerts(ctx context.Context, query AlertQuery) (*AlertPage, error)
	GetAlert(ctx context.Context, id int64) (*docker.Alert, error)
	ListAllowlists(ctx context.Context) ([]Allowlist, error)
	CheckAllowlist(ctx context.Context, value string) (bool, string, error)
	CreateAllowlist(ctx context.Context, name, description string) error
	AddAllowlistEntry(ctx context.Context, name, value, description, expiration string) error
	RemoveAllowlistEntry(ctx context.Context, name, value string) error
	DeleteAllowlist(ctx context.Context, name string) error
	ListHubItems(ctx context.Context) ([]docker.HubItem, error)
	ConfigureConnection(ctx context.Context, in ConnectionSettings, opts models.WriteOptions) (*models.WriteResult[CrowdSecConfig], error)
	TestConnection(ctx context.Context, in ConnectionSettings) (*ConnectionTest, error)
	Metrics(ctx context.Context) *MetricsSnapshot
	Status(ctx context.Context, inventory Inventory) (*CrowdSecStatus, error)
	Stats(ctx context.Context, windowHours int) (*CrowdSecStats, error)
}

var nameRegex = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
var machineNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.@:\[\]-]{3,96}$`)

var generatedSecretPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// generatedSecret is the credential registered through the container: the one the form proposed (shown
// to the person before applying) when it has the generated shape, otherwise a fresh random one.
func generatedSecret(proposed string) (string, error) {
	if proposed = strings.TrimSpace(proposed); generatedSecretPattern.MatchString(proposed) {
		return proposed, nil
	}
	return randomIntegrationSecret()
}

func randomIntegrationSecret() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return hex.EncodeToString(key), nil
}

func (m *crowdsecManager) GetConfig(ctx context.Context) (*CrowdSecConfig, error) {
	row, e := m.store.GetItem(ctx, models.KindCrowdSec, "integration")
	if e != nil {
		return nil, e
	}
	cfg := &CrowdSecConfig{IntegrationMode: "managed", Mode: "stream", LAPIURL: m.defaultLAPIURL, PluginVersion: crowdsecDefaultVersion, State: "disabled", HTTPTimeoutSeconds: 10, AppSecBodyLimit: 10 * 1024 * 1024, AppSecURL: m.settings.AppSecURL, ReadTimeoutSeconds: int(m.settings.ReadTimeout.Seconds()), AlertLimit: m.settings.AlertLimit}
	if row == nil {
		m.overlayEnvironment(cfg)
		return cfg, nil
	}
	if json.Unmarshal(row.Payload, cfg) != nil {
		return nil, ErrCrowdSecInvalidKey
	}
	cfg.LAPIKey = "••••••••"
	if cfg.MachineID != "" {
		cfg.MachinePassword = "••••••••"
	}
	if cfg.IntegrationMode == "" {
		cfg.IntegrationMode = "managed"
	}
	if m.defaultLAPIURL != "" {
		cfg.LAPIURL = m.defaultLAPIURL
	}
	if m.settings.ReadTimeoutSet || cfg.ReadTimeoutSeconds <= 0 {
		cfg.ReadTimeoutSeconds = int(m.settings.ReadTimeout.Seconds())
	}
	if m.settings.AlertLimitSet || cfg.AlertLimit <= 0 {
		cfg.AlertLimit = m.settings.AlertLimit
	}
	m.overlayEnvironment(cfg)
	// PEM is write-only through JSON. Presence is reported separately by Status sources.
	cfg.CACertificate, cfg.ClientCertificate, cfg.ClientKey = "", "", ""
	cfg.ActiveEnabled = false
	cfg.Version = row.Version
	if cfg.Enabled {
		cfg.State = "configured_unverified"
	} else {
		cfg.State = "disabled"
	}
	return cfg, nil
}
func (m *crowdsecManager) ConfigureBouncer(ctx context.Context, in CrowdSecConfig, opts models.WriteOptions) (*models.WriteResult[CrowdSecConfig], error) {
	return m.configure(ctx, in, opts, false)
}

// configure saves the whole integration. connection is true for ConfigureConnection: only then are the
// connection-only settings (metrics, access log source, protected networks, AppSec endpoint) taken from in;
// a protection save keeps the saved ones.
func (m *crowdsecManager) configure(ctx context.Context, in CrowdSecConfig, opts models.WriteOptions, connection bool) (*models.WriteResult[CrowdSecConfig], error) {
	candidateLAPIURL := strings.TrimSpace(in.LAPIURL)
	if candidateLAPIURL == "" {
		candidateLAPIURL = m.defaultLAPIURL
	}
	u, e := registeredURL(candidateLAPIURL)
	if e != nil {
		return nil, ErrCrowdSecUnreachable
	}
	if m.defaultLAPIURL != "" {
		registered, registeredErr := registeredURL(m.defaultLAPIURL)
		if registeredErr != nil || u.String() != registered.String() {
			return nil, ErrCrowdSecUnreachable
		}
	}
	in.LAPIURL = u.String()
	// Credentials entered with new settings are verified against the new address, not the saved one.
	ctx = withLAPIURL(ctx, in.LAPIURL)
	if in.Mode == "" {
		in.Mode = "stream"
	}
	if in.IntegrationMode == "" {
		in.IntegrationMode = "managed"
	}
	if in.IntegrationMode != "managed" && in.IntegrationMode != "external" {
		return nil, ErrCrowdSecInvalidKey
	}
	if in.Mode != "live" && in.Mode != "stream" && in.Mode != "alone" {
		return nil, ErrCrowdSecInvalidKey
	}
	if in.PluginVersion == "" {
		in.PluginVersion = crowdsecDefaultVersion
	}
	if !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(in.PluginVersion) {
		return nil, ErrCrowdSecInvalidKey
	}
	if in.HTTPTimeoutSeconds == 0 {
		in.HTTPTimeoutSeconds = 10
	}
	if in.HTTPTimeoutSeconds < 1 || in.HTTPTimeoutSeconds > 60 || in.UpdateMaxFailure < -1 || in.UpdateMaxFailure > 100 || len(in.Routers) > 32 {
		return nil, ErrCrowdSecInvalidKey
	}
	if in.ReadTimeoutSeconds == 0 {
		in.ReadTimeoutSeconds = int(m.settings.ReadTimeout.Seconds())
	}
	if m.settings.ReadTimeoutSet {
		in.ReadTimeoutSeconds = int(m.settings.ReadTimeout.Seconds())
	}
	if in.ReadTimeoutSeconds < 1 || in.ReadTimeoutSeconds > 120 {
		return nil, ErrCrowdSecInvalidKey
	}
	if in.AlertLimit == 0 {
		in.AlertLimit = m.settings.AlertLimit
	}
	if m.settings.AlertLimitSet {
		in.AlertLimit = m.settings.AlertLimit
	}
	if in.AlertLimit < 1 || in.AlertLimit > 1000 {
		return nil, ErrCrowdSecInvalidKey
	}
	var cidrsErr error
	in.TrustedProxyCIDRs, cidrsErr = boundedCIDRs(in.TrustedProxyCIDRs)
	if cidrsErr != nil {
		return nil, cidrsErr
	}
	in.ClientTrustedCIDRs, cidrsErr = boundedCIDRs(in.ClientTrustedCIDRs)
	if cidrsErr != nil {
		return nil, cidrsErr
	}
	if in.AppSecBodyLimit == 0 {
		in.AppSecBodyLimit = 10 * 1024 * 1024
	}
	if in.AppSecBodyLimit < 1 || in.AppSecBodyLimit > 64*1024*1024 {
		return nil, ErrCrowdSecInvalidKey
	}
	if in.AppSecFailureBlock == nil {
		in.AppSecFailureBlock = boolPointer(true)
	}
	if in.AppSecUnreachableBlock == nil {
		in.AppSecUnreachableBlock = boolPointer(true)
	}
	previousConnection, e := m.store.GetItem(ctx, models.KindCrowdSec, "integration")
	if e != nil {
		return nil, e
	}
	saved := CrowdSecConfig{}
	if previousConnection != nil && len(previousConnection.Payload) > 0 && json.Unmarshal(previousConnection.Payload, &saved) != nil {
		return nil, ErrCrowdSecInvalidKey
	}
	if !connection {
		if in.AppSecURL == "" {
			in.AppSecURL = saved.AppSecURL
		}
		in.MetricsURL, in.MetricsToken, in.AccessLogSource, in.ProtectedCIDRs = saved.MetricsURL, "", saved.AccessLogSource, saved.ProtectedCIDRs
	}
	in.MetricsTokenSet = saved.MetricsTokenSet
	if err := m.normalizeConnection(&in); err != nil {
		return nil, err
	}
	if in.AppSecEnabled && in.AppSecURL == "" {
		return nil, ErrCrowdSecUnreachable
	}
	if in.MiddlewareName == "" {
		in.MiddlewareName = crowdsecPluginShortName
	}
	if !nameRegex.MatchString(in.MiddlewareName) || in.PluginAlias != "" && !nameRegex.MatchString(in.PluginAlias) {
		return nil, ErrCrowdSecInvalidKey
	}
	mut := models.Mutation{}
	if connection && m.settings.MetricsToken == "" && newSecretInput(in.MetricsToken) {
		if err := m.prepareManagerSecret(ctx, &mut, crowdsecMetricsTokenSecret, "metrics_token", in.MetricsToken); err != nil {
			return nil, err
		}
		in.MetricsTokenSet = true
	}
	in.MetricsToken = ""
	in.KeepEnrollments = false
	old, e := m.store.GetItem(ctx, models.KindCrowdSec, "integration")
	if e != nil {
		return nil, e
	}
	previous := CrowdSecConfig{}
	if old != nil && len(old.Payload) > 0 {
		if json.Unmarshal(old.Payload, &previous) != nil {
			return nil, ErrCrowdSecInvalidKey
		}
	}
	in.EnrollmentIDs = previous.EnrollmentIDs
	in.MachineEnrollmentID = previous.MachineEnrollmentID
	if in.IntegrationMode == "external" {
		return m.configureExternal(ctx, in, mut, opts)
	}
	// Attach only explicitly selected MANAGED routers; preserve all unrelated order.
	rows, e := m.store.ListItemsByKind(ctx, models.KindRouter)
	if e != nil {
		return nil, e
	}
	selected := map[string]bool{}
	if in.Enabled {
		for _, name := range in.Routers {
			if selected[name] {
				return nil, ErrCrowdSecInvalidKey
			}
			selected[name] = true
		}
	}
	owned := map[string]bool{}
	for _, name := range previous.Routers {
		owned[name] = true
	}
	// Names previously attached under an older middleware name are detached too.
	previousMiddleware := previous.MiddlewareName
	if previousMiddleware == "" {
		previousMiddleware = crowdsecPluginShortName
	}
	for _, row := range rows {
		if !selected[row.Name] && !owned[row.Name] {
			continue
		}
		var router models.RouterConfig
		if json.Unmarshal(row.Payload, &router) != nil {
			return nil, ErrCrowdSecInvalidKey
		}
		middlewares := []string{}
		for _, mw := range router.Middlewares {
			if !hasMiddleware([]string{mw}, in.MiddlewareName) && !hasMiddleware([]string{mw}, previousMiddleware) {
				middlewares = append(middlewares, mw)
			}
		}
		if selected[row.Name] {
			middlewares = append(middlewares, in.MiddlewareName)
		}
		router.Middlewares = middlewares
		raw, _ := json.Marshal(router)
		mut.Upserts = append(mut.Upserts, models.ConfigItemRow{ID: "router:" + row.Name, Kind: models.KindRouter, Name: row.Name, Scope: models.ScopeDynamic, Payload: raw})
		delete(selected, row.Name)
	}
	if len(selected) > 0 {
		return nil, ErrCrowdSecInvalidKey
	}
	if in.Enabled {
		rawKey := strings.TrimSpace(in.LAPIKey)
		if m.settings.APIKey != "" {
			rawKey = m.settings.APIKey
			in.AutoEnroll = false
		}
		if in.AutoEnroll {
			rawKey, e = generatedSecret(in.LAPIKey)
			if e != nil {
				return nil, e
			}
		} else {
			if !newSecretInput(rawKey) {
				rawKey = m.bouncerCredential(ctx)
				if strings.TrimSpace(rawKey) == "" {
					return nil, ErrCrowdSecInvalidKey
				}
			}
			if e = m.verifyLAPIConnection(ctx, in.LAPIURL, rawKey); e != nil {
				return nil, e
			}
		}
		sec, e := m.secrets.PrepareSecret(ctx, crowdsecSecretName, "crowdsec_lapi", "", rawKey)
		if e != nil {
			return nil, e
		}
		mut.Secrets = append(mut.Secrets, *sec)
		machineID := strings.TrimSpace(in.MachineID)
		if machineID == "" {
			machineID = previous.MachineID
		}
		machinePassword := in.MachinePassword
		if m.settings.MachineID != "" && m.settings.MachinePassword != "" {
			in.AutoEnrollMachine = false
		}
		if !in.AutoEnrollMachine && !newSecretInput(machinePassword) {
			machinePassword, e = m.secrets.DecryptSecret(ctx, crowdsecMachineSecret)
			if e != nil {
				machinePassword = ""
			}
		}
		if m.settings.MachineID != "" {
			machineID = m.settings.MachineID
		}
		if m.settings.MachinePassword != "" {
			machinePassword = m.settings.MachinePassword
		}
		if in.AutoEnrollMachine {
			machinePassword, e = generatedSecret(in.MachinePassword)
			if e != nil {
				return nil, e
			}
			machineID = ""
		}
		// A machine created by the operation has no name yet: the container assigns it on apply.
		if !in.AutoEnrollMachine && (machineID == "") != (machinePassword == "") {
			return nil, ErrCrowdSecInvalidKey
		}
		if machineID != "" && !machineNamePattern.MatchString(machineID) {
			return nil, ErrCrowdSecInvalidKey
		}
		if machineID != "" && !in.AutoEnrollMachine {
			if e = m.verifyMachineCredentials(ctx, machineID, machinePassword); e != nil {
				return nil, e
			}
		}
		if machinePassword != "" {
			machineSecret, secretErr := m.secrets.PrepareSecret(ctx, crowdsecMachineSecret, "crowdsec_machine", "", machinePassword)
			if secretErr != nil {
				return nil, secretErr
			}
			mut.Secrets = append(mut.Secrets, *machineSecret)
		}
		in.MachineID = machineID
		// Add only what is missing: an existing bouncer declaration (managed under any alias, or one the caller
		// found outside the console) is reused as is, never overwritten or declared twice.
		pluginAlias := in.PluginAlias
		if pluginAlias != "" {
			existing, getErr := m.store.GetItem(ctx, models.KindPlugin, pluginAlias)
			if getErr != nil {
				return nil, getErr
			}
			if existing != nil {
				var declaration models.StaticPluginConfig
				if json.Unmarshal(existing.Payload, &declaration) != nil || !strings.EqualFold(declaration.ModuleName, crowdsecPluginModule) {
					return nil, ErrCrowdSecInvalidKey
				}
			}
		}
		if pluginAlias == "" {
			pluginAlias, e = m.installedPluginAlias(ctx)
			if e != nil {
				return nil, e
			}
		}
		if pluginAlias == "" {
			pluginAlias = crowdsecPluginShortName
			existing, e := m.store.GetItem(ctx, models.KindPlugin, crowdsecPluginShortName)
			if e != nil {
				return nil, e
			}
			if existing == nil {
				plug, _ := json.Marshal(models.StaticPluginConfig{ModuleName: crowdsecPluginModule, Version: in.PluginVersion})
				mut.Upserts = append(mut.Upserts, models.ConfigItemRow{ID: "plugin:" + crowdsecPluginShortName, Kind: models.KindPlugin, Name: crowdsecPluginShortName, Scope: models.ScopeStatic, Payload: plug})
			} else {
				var declaration models.StaticPluginConfig
				if json.Unmarshal(existing.Payload, &declaration) != nil || !strings.EqualFold(declaration.ModuleName, crowdsecPluginModule) {
					return nil, ErrCrowdSecInvalidKey
				}
			}
		}
		in.PluginAlias = pluginAlias
		// Keep settings the operator added to an existing compatible middleware; only guided keys are rewritten.
		spec := map[string]any{}
		currentMiddleware := models.MiddlewareConfig{Type: "plugin", PluginName: pluginAlias}
		in.MiddlewareOwned = true
		if mw, e := m.store.GetItem(ctx, models.KindMiddleware, in.MiddlewareName); e != nil {
			return nil, e
		} else if mw != nil {
			var current models.MiddlewareConfig
			if json.Unmarshal(mw.Payload, &current) != nil || current.Type != "plugin" || current.PluginName != pluginAlias {
				return nil, ErrCrowdSecInvalidKey
			}
			// Still Console-created when it is the middleware a previous save created.
			in.MiddlewareOwned = previous.MiddlewareOwned && (previous.MiddlewareName == "" || previous.MiddlewareName == in.MiddlewareName)
			currentMiddleware.SecretRefs = current.SecretRefs
			for k, v := range current.Spec {
				spec[k] = v
			}
		}
		for _, key := range []string{"crowdsecAppsecEnabled", "crowdsecAppsecScheme", "crowdsecAppsecHost", "crowdsecAppsecPath", "crowdsecAppsecKeyFile", "crowdsecAppsecFailureBlock", "crowdsecAppsecUnreachableBlock", "crowdsecAppsecBodyLimit"} {
			delete(spec, key)
		}
		for k, v := range map[string]any{"enabled": true, "crowdsecMode": in.Mode, "crowdsecLapiScheme": u.Scheme, "crowdsecLapiHost": u.Host, "crowdsecLapiKeyFile": sec.FilePath, "httpTimeoutSeconds": in.HTTPTimeoutSeconds, "updateIntervalSeconds": 60, "updateMaxFailure": in.UpdateMaxFailure, "forwardedHeadersTrustedIPs": in.TrustedProxyCIDRs, "clientTrustedIPs": in.ClientTrustedCIDRs} {
			spec[k] = v
		}
		if in.AppSecEnabled {
			appSecURL, _ := registeredURL(in.AppSecURL)
			for k, v := range map[string]any{"crowdsecAppsecEnabled": true, "crowdsecAppsecScheme": appSecURL.Scheme, "crowdsecAppsecHost": appSecURL.Host, "crowdsecAppsecPath": "/", "crowdsecAppsecKeyFile": sec.FilePath, "crowdsecAppsecFailureBlock": *in.AppSecFailureBlock, "crowdsecAppsecUnreachableBlock": *in.AppSecUnreachableBlock, "crowdsecAppsecBodyLimit": in.AppSecBodyLimit} {
				spec[k] = v
			}
		}
		currentMiddleware.Spec = spec
		raw, _ := json.Marshal(currentMiddleware)
		mut.Upserts = append(mut.Upserts, models.ConfigItemRow{ID: "middleware:" + in.MiddlewareName, Kind: models.KindMiddleware, Name: in.MiddlewareName, Scope: models.ScopeDynamic, Payload: raw})
	} else {
		// The Console keeps reading decisions with its registered key after protection is turned off, so the
		// key stays registered (and may be generated) while the integration is disabled.
		in.KeepEnrollments = true
		in.Routers = nil
		if e = m.prepareConnectionCredentials(ctx, &mut, &in); e != nil {
			return nil, e
		}
		// Shared declarations and reused middleware are never deleted. A reused
		// middleware is disabled in place; only a Console-created one is removed.
		if old != nil {
			mw, e := m.store.GetItem(ctx, models.KindMiddleware, previousMiddleware)
			if e != nil {
				return nil, e
			}
			if mw != nil && previous.MiddlewareOwned {
				mut.Deletes = append(mut.Deletes, models.ItemRef{Kind: models.KindMiddleware, Name: previousMiddleware})
			} else if mw != nil {
				var current models.MiddlewareConfig
				if json.Unmarshal(mw.Payload, &current) != nil {
					return nil, ErrCrowdSecInvalidKey
				}
				if current.Spec == nil {
					current.Spec = map[string]any{}
				} else {
					copySpec := map[string]any{}
					for k, v := range current.Spec {
						copySpec[k] = v
					}
					current.Spec = copySpec
				}
				current.Spec["enabled"] = false
				raw, _ := json.Marshal(current)
				mut.Upserts = append(mut.Upserts, models.ConfigItemRow{ID: "middleware:" + previousMiddleware, Kind: models.KindMiddleware, Name: previousMiddleware, Scope: models.ScopeDynamic, Payload: raw})
			}
		}
	}
	if e = m.preparePEMInputs(ctx, &mut, in); e != nil {
		return nil, e
	}
	in.ActiveEnabled = false
	in.Version = 0
	in.LAPIKey = ""
	in.MachinePassword = ""
	in.CACertificate, in.ClientCertificate, in.ClientKey = "", "", ""
	in.State = ""
	raw, _ := json.Marshal(in)
	mut.Upserts = append(mut.Upserts, models.ConfigItemRow{ID: "crowdsec:integration", Kind: models.KindCrowdSec, Name: "integration", Scope: models.ScopeDynamic, Payload: raw})
	return engine.Write(ctx, m.applier, mut, opts, func(ctx context.Context) (*models.UIResource[CrowdSecConfig], error) {
		cfg, e := m.GetConfig(ctx)
		if e != nil {
			return nil, e
		}
		return &models.UIResource[CrowdSecConfig]{ID: "crowdsec:integration", Name: "integration", Origin: "managed", Provider: "dynamic", Status: models.StatusActive, Config: *cfg, Version: cfg.Version}, nil
	})
}

// ListDecisions uses native LAPI credentials when present, then the typed companion
// capability for legacy installations. No CLI format leaks into core.
func (m *crowdsecManager) ListDecisions(ctx context.Context) ([]CrowdSecDecision, error) {
	bouncer, err := m.hasBouncerIdentity(ctx)
	if err != nil {
		return nil, err
	}
	_, _, machinePresent := m.machineCredentials(ctx)
	certificate, err := m.hasClientCertificate(ctx, true)
	if err != nil {
		return nil, err
	}
	if bouncer || machinePresent || certificate {
		return m.listDecisionsNative(ctx)
	}
	// The registered engine lists local decisions only when native authentication is absent.
	if m.decisions != nil {
		return m.decisions.ListDecisions(ctx)
	}
	return nil, ErrCrowdSecUnavailableCapability
}

func (m *crowdsecManager) AddDecision(ctx context.Context, ipOrCIDR, duration, reason string) error {
	ipOrCIDR = strings.TrimSpace(ipOrCIDR)
	if !isValidIPOrCIDR(ipOrCIDR) {
		return ErrCrowdSecInvalidIP
	}
	// Protect operator-registered management networks, including CIDR overlaps.
	ip := net.ParseIP(ipOrCIDR)
	_, network, _ := net.ParseCIDR(ipOrCIDR)
	for _, protected := range m.effectiveProtected(ctx) {
		if (ip != nil && protected.Contains(ip)) || (network != nil && (network.Contains(protected.IP) || protected.Contains(network.IP))) {
			return ErrCrowdSecInvalidIP
		}
	}
	if duration == "" {
		duration = "4h"
	}
	d, e := time.ParseDuration(duration)
	if e != nil || d <= 0 || d > 7*24*time.Hour || len(reason) > 256 || strings.ContainsAny(reason, "\x00\r\n") {
		return ErrCrowdSecInvalidIP
	}
	if reason == "" {
		reason = "manual_console_ban"
	}
	err := m.decisions.AddDecision(ctx, ipOrCIDR, duration, reason)
	return err
}

func (m *crowdsecManager) DeleteDecision(ctx context.Context, ipOrCIDR string) error {
	ipOrCIDR = strings.TrimSpace(ipOrCIDR)
	if !isValidIPOrCIDR(ipOrCIDR) {
		return ErrCrowdSecInvalidIP
	}
	err := m.decisions.DeleteDecision(ctx, ipOrCIDR)
	return err
}

func (m *crowdsecManager) verifyLAPIConnection(ctx context.Context, lapiURL, apiKey string) error {
	u, err := registeredURL(lapiURL)
	if err != nil {
		return ErrCrowdSecUnreachable
	}
	if m.defaultLAPIURL != "" {
		registered, registeredErr := registeredURL(m.defaultLAPIURL)
		if registeredErr != nil || u.String() != registered.String() {
			return ErrCrowdSecUnreachable
		}
	}
	client := m.httpClient
	if client == nil {
		client, err = m.clientForURL(ctx, lapiURL, false)
	}
	if err != nil {
		return err
	}
	if m.httpClient == nil {
		defer client.CloseIdleConnections()
	}
	// Explicit rejection is retained even for injected test transports.
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, lapiURL+"/v1/decisions?ip=127.0.0.1", nil)
	if err != nil {
		return ErrCrowdSecUnreachable
	}
	if apiKey != "" {
		req.Header.Set("X-Api-Key", apiKey)
	}
	req.Header.Set("User-Agent", project.UserAgent())

	resp, err := clientCopy.Do(req)
	if err != nil {
		return ErrCrowdSecUnreachable
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		return ErrCrowdSecInvalidKey
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: HTTP %d", ErrCrowdSecUnreachable, resp.StatusCode)
	}
	return nil
}

func isValidIPOrCIDR(val string) bool {
	if net.ParseIP(val) != nil {
		return true
	}
	_, _, err := net.ParseCIDR(val)
	return err == nil
}

func boolPointer(value bool) *bool { return &value }

func boundedCIDRs(values []string) ([]string, error) {
	if len(values) > 16 {
		return nil, ErrCrowdSecInvalidIP
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, raw := range values {
		ip, network, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err != nil || ip == nil || network == nil || ip.IsUnspecified() {
			return nil, ErrCrowdSecInvalidIP
		}
		canonical := network.String()
		if seen[canonical] {
			return nil, ErrCrowdSecInvalidIP
		}
		seen[canonical] = true
		out = append(out, canonical)
	}
	return out, nil
}

func (m *crowdsecManager) VerifyKey(ctx context.Context, key string) error {
	lapiURL, err := m.effectiveLAPIURL(ctx)
	if err != nil {
		return err
	}
	return m.verifyLAPIConnection(ctx, lapiURL, key)
}

// VerifyKeyAt verifies a key against the address an operation is about to save (still checked against the
// registered address when the environment sets one).
func (m *crowdsecManager) VerifyKeyAt(ctx context.Context, lapiURL, key string) error {
	return m.verifyLAPIConnection(ctx, lapiURL, key)
}

// VerifyMachineAt verifies machine credentials against the address an operation is about to save.
func (m *crowdsecManager) VerifyMachineAt(ctx context.Context, lapiURL, machineID, password string) error {
	return m.verifyMachineCredentials(withLAPIURL(ctx, lapiURL), machineID, password)
}

func (m *crowdsecManager) VerifyMachine(ctx context.Context, machineID, password string) error {
	return m.verifyMachineCredentials(ctx, machineID, password)
}

func newSecretInput(value string) bool {
	return strings.TrimSpace(value) != "" && !strings.HasPrefix(value, "••••••••")
}

func (m *crowdsecManager) prepareManagerSecret(ctx context.Context, mut *models.Mutation, name, purpose, value string) error {
	if !newSecretInput(value) {
		return nil
	}
	sec, err := m.secrets.PrepareSecret(ctx, name, managerSecretPurpose+"_"+purpose, "", strings.TrimSpace(value))
	if err != nil {
		return err
	}
	mut.Secrets = append(mut.Secrets, *sec)
	return nil
}

func (m *crowdsecManager) environmentClientCertificate(machine bool) bool {
	cert, key := m.settings.BouncerCertFile, m.settings.BouncerKeyFile
	if machine {
		cert, key = m.settings.MachineCertFile, m.settings.MachineKeyFile
	}
	return cert != "" && key != ""
}

func (m *crowdsecManager) preparePEMInputs(ctx context.Context, mut *models.Mutation, in CrowdSecConfig) error {
	certInput, keyInput := newSecretInput(in.ClientCertificate), newSecretInput(in.ClientKey)
	if certInput != keyInput {
		return ErrCrowdSecInvalidKey
	}
	if newSecretInput(in.CACertificate) || certInput {
		if _, err := tlsConfigFromPEM([]byte(in.CACertificate), []byte(in.ClientCertificate), []byte(in.ClientKey)); err != nil {
			return err
		}
	}
	if m.settings.CAFile == "" {
		if err := m.prepareManagerSecret(ctx, mut, crowdsecCACertSecret, "ca", in.CACertificate); err != nil {
			return err
		}
	}
	if !m.environmentClientCertificate(false) && !m.environmentClientCertificate(true) {
		if err := m.prepareManagerSecret(ctx, mut, crowdsecClientCertName, "client_cert", in.ClientCertificate); err != nil {
			return err
		}
		if err := m.prepareManagerSecret(ctx, mut, crowdsecClientKeySecret, "client_key", in.ClientKey); err != nil {
			return err
		}
	}
	return nil
}

func (m *crowdsecManager) configureExternal(ctx context.Context, in CrowdSecConfig, mut models.Mutation, opts models.WriteOptions) (*models.WriteResult[CrowdSecConfig], error) {
	in.Enabled = false
	in.Routers = nil
	if err := m.prepareConnectionCredentials(ctx, &mut, &in); err != nil {
		return nil, err
	}
	if err := m.preparePEMInputs(ctx, &mut, in); err != nil {
		return nil, err
	}
	in.ActiveEnabled = false
	in.Version = 0
	in.LAPIKey, in.MachinePassword = "", ""
	in.CACertificate, in.ClientCertificate, in.ClientKey = "", "", ""
	in.State = ""
	raw, _ := json.Marshal(in)
	mut.Upserts = append(mut.Upserts, models.ConfigItemRow{ID: "crowdsec:integration", Kind: models.KindCrowdSec, Name: "integration", Scope: models.ScopeDynamic, Payload: raw})
	return engine.Write(ctx, m.applier, mut, opts, func(ctx context.Context) (*models.UIResource[CrowdSecConfig], error) {
		cfg, err := m.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		return &models.UIResource[CrowdSecConfig]{ID: "crowdsec:integration", Name: "integration", Origin: "managed", Provider: "dynamic", Status: models.StatusActive, Config: *cfg, Version: cfg.Version}, nil
	})
}

// prepareConnectionCredentials stages the bouncer key and machine credentials when protection is not
// managed by this save (external mode, or disabled). A requested key is generated here and registered by
// the operation through the fixed container capability; an entered one is verified first.
func (m *crowdsecManager) prepareConnectionCredentials(ctx context.Context, mut *models.Mutation, in *CrowdSecConfig) error {
	newTLSMaterial := newSecretInput(in.CACertificate) || newSecretInput(in.ClientCertificate) || newSecretInput(in.ClientKey)
	if m.settings.APIKey != "" {
		in.AutoEnroll = false
	}
	switch {
	case in.AutoEnroll:
		if !m.canGenerateKey(ctx) {
			return ErrCrowdSecUnavailableCapability
		}
		key, err := generatedSecret(in.LAPIKey)
		if err != nil {
			return err
		}
		sec, err := m.secrets.PrepareSecret(ctx, crowdsecSecretName, "crowdsec_lapi", "", key)
		if err != nil {
			return err
		}
		mut.Secrets = append(mut.Secrets, *sec)
	default:
		rawKey := strings.TrimSpace(in.LAPIKey)
		if m.settings.APIKey != "" {
			rawKey = m.settings.APIKey
		}
		entered := m.settings.APIKey == "" && newSecretInput(in.LAPIKey)
		if !newSecretInput(rawKey) {
			rawKey = m.bouncerCredential(ctx)
		}
		// A saved key is not re-verified on every save: an unreachable engine must not block other settings.
		if entered && !newTLSMaterial {
			if err := m.verifyLAPIConnection(ctx, in.LAPIURL, rawKey); err != nil {
				return err
			}
		}
		if entered {
			if err := m.prepareManagerSecret(ctx, mut, crowdsecSecretName, "lapi", in.LAPIKey); err != nil {
				return err
			}
		}
	}

	if m.settings.MachineID != "" || m.settings.MachinePassword != "" {
		in.AutoEnrollMachine = false
	}
	if in.AutoEnrollMachine {
		// The operation registers the machine through the container and records its generated name.
		if !m.canRegisterMachine(ctx) {
			return ErrCrowdSecUnavailableCapability
		}
		password, err := generatedSecret(in.MachinePassword)
		if err != nil {
			return err
		}
		sec, err := m.secrets.PrepareSecret(ctx, crowdsecMachineSecret, "crowdsec_machine", "", password)
		if err != nil {
			return err
		}
		mut.Secrets = append(mut.Secrets, *sec)
		in.MachineID = ""
		return nil
	}
	machineID, machinePassword, _ := m.machineCredentials(ctx)
	savedMachineID := machineID
	if strings.TrimSpace(in.MachineID) != "" || m.settings.MachineID == "" && newSecretInput(in.MachinePassword) {
		machineID = strings.TrimSpace(in.MachineID)
	}
	if m.settings.MachineID != "" {
		machineID = m.settings.MachineID
	}
	enteredPassword := m.settings.MachinePassword == "" && newSecretInput(in.MachinePassword)
	if enteredPassword {
		machinePassword = in.MachinePassword
	}
	if m.settings.MachinePassword != "" {
		machinePassword = m.settings.MachinePassword
	}
	if (machineID == "") != (machinePassword == "") || machineID != "" && !machineNamePattern.MatchString(machineID) {
		return ErrCrowdSecInvalidKey
	}
	if machineID != "" && (enteredPassword || machineID != savedMachineID) && !newTLSMaterial {
		if err := m.verifyMachineCredentials(ctx, machineID, machinePassword); err != nil {
			return err
		}
	}
	if enteredPassword {
		if err := m.prepareManagerSecret(ctx, mut, crowdsecMachineSecret, "machine_password", in.MachinePassword); err != nil {
			return err
		}
	}
	in.MachineID = machineID
	return nil
}

// overlayEnvironment applies the startup settings that lock their Console counterparts.
func (m *crowdsecManager) overlayEnvironment(cfg *CrowdSecConfig) {
	if m.settings.AppSecURL != "" {
		cfg.AppSecURL = m.settings.AppSecURL
	}
	if m.settings.MetricsURL != "" {
		cfg.MetricsURL = m.settings.MetricsURL
	}
	if m.settings.AccessLogSource != "" {
		cfg.AccessLogSource = m.settings.AccessLogSource
	}
	if m.settings.MachineID != "" {
		cfg.MachineID = m.settings.MachineID
	}
	if m.settings.ProtectedCIDRsSet || len(cfg.ProtectedCIDRs) == 0 {
		cfg.ProtectedCIDRs = m.defaultProtectedCIDRs()
	}
	cfg.MetricsTokenSet = cfg.MetricsTokenSet || m.settings.MetricsToken != ""
	cfg.MetricsToken = ""
	cfg.KeepEnrollments = false
}

// normalizeConnection validates the connection-only settings; locked ones take the environment value.
func (m *crowdsecManager) normalizeConnection(in *CrowdSecConfig) error {
	if m.settings.AppSecURL != "" {
		in.AppSecURL = m.settings.AppSecURL
	}
	if in.AppSecURL != "" {
		u, err := registeredURL(in.AppSecURL)
		if err != nil {
			return ErrCrowdSecUnreachable
		}
		in.AppSecURL = u.String()
	}
	if m.settings.MetricsURL != "" {
		in.MetricsURL = ""
	} else if in.MetricsURL != "" {
		u, err := registeredMetricsURL(in.MetricsURL)
		if err != nil {
			return ErrCrowdSecUnreachable
		}
		in.MetricsURL = u.String()
	}
	if m.settings.AccessLogSource != "" {
		in.AccessLogSource = ""
	} else if !validAccessLogSource(in.AccessLogSource) {
		return ErrCrowdSecInvalidKey
	}
	if m.settings.ProtectedCIDRsSet {
		in.ProtectedCIDRs = nil
		return nil
	}
	cidrs, err := boundedCIDRs(in.ProtectedCIDRs)
	if err != nil {
		return err
	}
	in.ProtectedCIDRs = cidrs
	return nil
}

// installedPluginAlias returns the alias of a managed bouncer plugin (a pending removal does not count)
// that is not the CrowdSec default one, or "" when the default alias should be declared.
func (m *crowdsecManager) installedPluginAlias(ctx context.Context) (string, error) {
	rows, err := m.store.ListItemsByKind(ctx, models.KindPlugin)
	if err != nil {
		return "", err
	}
	alias := ""
	for _, row := range rows {
		if row.Kind != models.KindPlugin {
			continue
		}
		var cfg models.StaticPluginConfig
		if json.Unmarshal(row.Payload, &cfg) != nil || !strings.EqualFold(cfg.ModuleName, crowdsecPluginModule) {
			continue
		}
		if row.Name == crowdsecPluginShortName {
			return "", nil
		}
		if alias == "" || row.Name < alias {
			alias = row.Name
		}
	}
	return alias, nil
}

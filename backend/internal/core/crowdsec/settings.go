package crowdsec

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/traefik-console/internal/docker"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/project"
)

const (
	crowdsecMetricsTokenSecret = "crowdsec_metrics_token"
	maxAccessLogSourceLength   = 256
)

// EnvVariable is one integration variable the operator set at startup. The matching Console setting is
// read-only while it is set. Value is empty for secrets.
type EnvVariable struct {
	Name   string `json:"name"`
	Field  string `json:"field"`
	Secret bool   `json:"secret"`
	Value  string `json:"value,omitempty"`
}

// RuntimeRegistration is the container registration the Console was started with. It is shared with the
// Traefik runtime operations, so it stays startup configuration (DOCKER_HOST, CROWDSEC_CONTAINER).
// ContainerID carries the registered container name.
type RuntimeRegistration struct {
	// Accessible is true when the registered CrowdSec container is running and the Console can run its
	// fixed cscli operations (create a bouncer key, create monitoring credentials).
	Accessible       bool   `json:"accessible"`
	DockerConfigured bool   `json:"docker_configured"`
	ContainerID      string `json:"container_id,omitempty"`
	ComposeService   string `json:"compose_service,omitempty"`
}

// EnvironmentField maps each CROWDSEC_* variable to the Console setting it locks, and whether its value is
// a secret. Variables that name files (certificates) are locked but their path is not a secret.
var EnvironmentField = map[string]struct {
	Field  string
	Secret bool
}{
	"CROWDSEC_LAPI_URL":          {"lapi_url", false},
	"CROWDSEC_API_KEY":           {"lapi_key", true},
	"CROWDSEC_MACHINE_ID":        {"machine_id", false},
	"CROWDSEC_MACHINE_PASSWORD":  {"machine_password", true},
	"CROWDSEC_CA_CERT":           {"ca_certificate", false},
	"CROWDSEC_CLIENT_CERT":       {"client_certificate", false},
	"CROWDSEC_CLIENT_KEY":        {"client_key", false},
	"CROWDSEC_BOUNCER_CERT":      {"client_certificate", false},
	"CROWDSEC_BOUNCER_KEY":       {"client_key", false},
	"CROWDSEC_MACHINE_CERT":      {"client_certificate", false},
	"CROWDSEC_MACHINE_KEY":       {"client_key", false},
	"CROWDSEC_APPSEC_URL":        {"appsec_url", false},
	"CROWDSEC_METRICS_URL":       {"metrics_url", false},
	"CROWDSEC_METRICS_TOKEN":     {"metrics_token", true},
	"CROWDSEC_ACCESS_LOG_SOURCE": {"access_log_source", false},
	"CROWDSEC_READ_TIMEOUT":      {"read_timeout_seconds", false},
	"CROWDSEC_ALERT_LIMIT":       {"alert_limit", false},
	"CROWDSEC_PROTECTED_CIDRS":   {"protected_cidrs", false},
	"CROWDSEC_CONTAINER":         {"container_id", false},
}

// ConnectionSettings is everything the Console needs to reach CrowdSec. It is saved independently of the
// protection (plugin, middleware, routers); a field locked by the environment is ignored.
type ConnectionSettings struct {
	LAPIURL     string `json:"lapi_url"`
	LAPIKey     string `json:"lapi_key,omitempty"`
	GenerateKey bool   `json:"generate_key,omitempty"`
	// GenerateMachine registers monitoring (watcher) credentials through the CrowdSec container.
	GenerateMachine    bool     `json:"generate_machine,omitempty"`
	MachineID          string   `json:"machine_id,omitempty"`
	MachinePassword    string   `json:"machine_password,omitempty"`
	CACertificate      string   `json:"ca_certificate,omitempty"`
	ClientCertificate  string   `json:"client_certificate,omitempty"`
	ClientKey          string   `json:"client_key,omitempty"`
	AppSecURL          string   `json:"appsec_url,omitempty"`
	MetricsURL         string   `json:"metrics_url,omitempty"`
	MetricsToken       string   `json:"metrics_token,omitempty"`
	AccessLogSource    string   `json:"access_log_source,omitempty"`
	ReadTimeoutSeconds int      `json:"read_timeout_seconds,omitempty"`
	AlertLimit         int      `json:"alert_limit,omitempty"`
	ProtectedCIDRs     []string `json:"protected_cidrs,omitempty"`
	// Protection, when present, also sets how Traefik is protected; absent keeps the protection as saved.
	Protection *ProtectionSettings `json:"protection,omitempty"`
}

// ProtectionSettings is the bouncer part of the single CrowdSec settings form. Router attachments are not
// part of it: routers keep the bouncer middleware they already carry.
type ProtectionSettings struct {
	// Mode is off (no bouncer managed by the Console), managed (the Console installs and configures the
	// bouncer plugin and middleware) or external (Traefik is protected outside the Console).
	Mode               string   `json:"mode"`
	BouncerMode        string   `json:"bouncer_mode,omitempty"`
	PluginVersion      string   `json:"plugin_version,omitempty"`
	PluginAlias        string   `json:"plugin_alias,omitempty"`
	MiddlewareName     string   `json:"middleware_name,omitempty"`
	UpdateMaxFailure   int      `json:"update_max_failure"`
	HTTPTimeoutSeconds int      `json:"http_timeout_seconds,omitempty"`
	AppSecEnabled      bool     `json:"appsec_enabled"`
	TrustedProxyCIDRs  []string `json:"trusted_proxy_cidrs,omitempty"`
	ClientTrustedCIDRs []string `json:"client_trusted_cidrs,omitempty"`
}

// ConnectionTest is the result of probing candidate connection settings without saving them.
type ConnectionTest struct {
	LAPIURL string `json:"lapi_url"`
	// LAPI is ok | reachable (no credential to check) | invalid_key | unreachable | not_configured.
	LAPI string `json:"lapi"`
	// Machine is ok | invalid_key | unreachable | not_configured.
	Machine string `json:"machine"`
	// AppSec is reachable | unreachable | not_configured.
	AppSec string `json:"appsec"`
	// Metrics is available | authentication_rejected | unavailable | not_configured.
	Metrics string `json:"metrics"`
	// Engine is the registered container state: running | stopped | not_configured | unavailable.
	Engine         string `json:"engine"`
	CanGenerateKey bool   `json:"can_generate_key"`
	ObservedAt     string `json:"observed_at"`
}

type lapiURLOverride struct{}

// withLAPIURL makes LAPI calls in ctx use a candidate address (already validated against the registered
// one) instead of the saved one, so new settings can be verified before they are committed.
func withLAPIURL(ctx context.Context, lapiURL string) context.Context {
	if strings.TrimSpace(lapiURL) == "" {
		return ctx
	}
	return context.WithValue(ctx, lapiURLOverride{}, lapiURL)
}

func overrideLAPIURL(ctx context.Context) string {
	value, _ := ctx.Value(lapiURLOverride{}).(string)
	return value
}

func (m *crowdsecManager) environmentSet(name string) bool {
	for _, variable := range m.settings.Environment {
		if variable.Name == name {
			return true
		}
	}
	return false
}

// effectiveAppSecURL, effectiveMetrics and effectiveAccessLogSource prefer the environment, then saved settings.
func (m *crowdsecManager) effectiveAppSecURL(ctx context.Context) string {
	if m.settings.AppSecURL != "" {
		return m.settings.AppSecURL
	}
	if cfg := m.storedConfig(ctx); cfg != nil {
		return cfg.AppSecURL
	}
	return ""
}

// storedConfig is GetConfig for the effective-setting helpers; nil when nothing can be read.
func (m *crowdsecManager) storedConfig(ctx context.Context) *CrowdSecConfig {
	if m.store == nil {
		return nil
	}
	cfg, err := m.GetConfig(ctx)
	if err != nil {
		return nil
	}
	return cfg
}

func (m *crowdsecManager) effectiveMetrics(ctx context.Context) (string, string) {
	metricsURL, token := m.settings.MetricsURL, m.settings.MetricsToken
	if metricsURL == "" {
		if cfg := m.storedConfig(ctx); cfg != nil {
			metricsURL = cfg.MetricsURL
		}
	}
	if token == "" && metricsURL != "" && m.secrets != nil {
		if saved, err := m.secrets.DecryptSecret(ctx, crowdsecMetricsTokenSecret); err == nil {
			token = strings.TrimSpace(saved)
		}
	}
	return metricsURL, token
}

func (m *crowdsecManager) effectiveAccessLogSource(ctx context.Context) string {
	if m.settings.AccessLogSource != "" {
		return m.settings.AccessLogSource
	}
	if cfg := m.storedConfig(ctx); cfg != nil {
		return cfg.AccessLogSource
	}
	return ""
}

// effectiveProtected returns the networks a manual ban may never cover: the environment list when set,
// otherwise the saved list, otherwise the defaults.
func (m *crowdsecManager) effectiveProtected(ctx context.Context) []*net.IPNet {
	if m.settings.ProtectedCIDRsSet {
		return m.protected
	}
	cfg := m.storedConfig(ctx)
	if cfg == nil || len(cfg.ProtectedCIDRs) == 0 {
		return m.protected
	}
	nets := make([]*net.IPNet, 0, len(cfg.ProtectedCIDRs))
	for _, raw := range cfg.ProtectedCIDRs {
		if _, network, e := net.ParseCIDR(raw); e == nil {
			nets = append(nets, network)
		}
	}
	if len(nets) == 0 {
		return m.protected
	}
	return nets
}

func (m *crowdsecManager) defaultProtectedCIDRs() []string {
	out := make([]string, 0, len(m.protected))
	for _, network := range m.protected {
		out = append(out, network.String())
	}
	return out
}

// canGenerateKey is true when a bouncer key can be registered through the fixed CrowdSec container.
func (m *crowdsecManager) canGenerateKey(ctx context.Context) bool {
	if m.settings.APIKey != "" {
		return false
	}
	if _, ok := m.decisions.(docker.BouncerRegistrar); !ok {
		return false
	}
	inspector, ok := m.decisions.(docker.CrowdSecInspector)
	if !ok {
		return false
	}
	engine, err := inspector.CrowdSecEngine(ctx)
	return err == nil && engine.Configured && engine.Running
}

// canRegisterMachine is true when watcher credentials can be registered through the running container.
func (m *crowdsecManager) canRegisterMachine(ctx context.Context) bool {
	if _, ok := m.decisions.(docker.MachineRegistrar); !ok {
		return false
	}
	return m.engineState(ctx) == "running"
}

func (m *crowdsecManager) engineState(ctx context.Context) string {
	inspector, ok := m.decisions.(docker.CrowdSecInspector)
	if !ok {
		return "unavailable"
	}
	engine, err := inspector.CrowdSecEngine(ctx)
	switch {
	case err != nil:
		return "unavailable"
	case !engine.Configured:
		return "not_configured"
	case engine.Running:
		return "running"
	default:
		return "stopped"
	}
}

func validAccessLogSource(value string) bool {
	return len(value) <= maxAccessLogSourceLength && !strings.ContainsAny(value, "\x00\r\n\"")
}

// ConfigureConnection saves the connection settings and keeps the protection exactly as it is. It is a
// normal integration write (same transaction, preview and rollback as the protection setup).
func (m *crowdsecManager) ConfigureConnection(ctx context.Context, in ConnectionSettings, opts models.WriteOptions) (*models.WriteResult[CrowdSecConfig], error) {
	current, err := m.savedConfig(ctx)
	if err != nil {
		return nil, err
	}
	current.LAPIURL = strings.TrimSpace(in.LAPIURL)
	current.LAPIKey = in.LAPIKey
	current.AutoEnroll = in.GenerateKey
	current.AutoEnrollMachine = in.GenerateMachine
	current.MachineID = strings.TrimSpace(in.MachineID)
	current.MachinePassword = in.MachinePassword
	current.CACertificate, current.ClientCertificate, current.ClientKey = in.CACertificate, in.ClientCertificate, in.ClientKey
	current.AppSecURL = strings.TrimSpace(in.AppSecURL)
	current.MetricsURL = strings.TrimSpace(in.MetricsURL)
	current.MetricsToken = in.MetricsToken
	current.AccessLogSource = strings.TrimSpace(in.AccessLogSource)
	current.ReadTimeoutSeconds = in.ReadTimeoutSeconds
	current.AlertLimit = in.AlertLimit
	current.ProtectedCIDRs = in.ProtectedCIDRs
	if p := in.Protection; p != nil {
		switch p.Mode {
		case "off":
			current.IntegrationMode, current.Enabled = "managed", false
		case "managed":
			current.IntegrationMode, current.Enabled = "managed", true
		case "external":
			current.IntegrationMode, current.Enabled = "external", false
		default:
			return nil, ErrCrowdSecInvalidKey
		}
		if p.BouncerMode != "" {
			current.Mode = p.BouncerMode
		}
		if p.PluginVersion != "" {
			current.PluginVersion = p.PluginVersion
		}
		if p.PluginAlias != "" {
			current.PluginAlias = p.PluginAlias
		}
		if p.MiddlewareName != "" {
			current.MiddlewareName = p.MiddlewareName
		}
		current.UpdateMaxFailure = p.UpdateMaxFailure
		if p.HTTPTimeoutSeconds != 0 {
			current.HTTPTimeoutSeconds = p.HTTPTimeoutSeconds
		}
		current.AppSecEnabled = p.AppSecEnabled
		current.TrustedProxyCIDRs, current.ClientTrustedCIDRs = p.TrustedProxyCIDRs, p.ClientTrustedCIDRs
	}
	// Routers keep their attachment; one deleted since the last save is simply dropped.
	if len(current.Routers) > 0 {
		rows, err := m.store.ListItemsByKind(ctx, models.KindRouter)
		if err != nil {
			return nil, err
		}
		existing := map[string]bool{}
		for _, row := range rows {
			existing[row.Name] = true
		}
		kept := current.Routers[:0]
		for _, name := range current.Routers {
			if existing[name] {
				kept = append(kept, name)
			}
		}
		current.Routers = kept
	}
	if current.AppSecEnabled && current.AppSecURL == "" && m.settings.AppSecURL == "" {
		// AppSec protection needs an endpoint: it cannot be removed while protection uses it.
		return nil, ErrCrowdSecUnreachable
	}
	return m.configure(ctx, *current, opts, true)
}

// savedConfig is the stored integration with secrets left out, or the defaults before the first save.
func (m *crowdsecManager) savedConfig(ctx context.Context) (*CrowdSecConfig, error) {
	cfg, err := m.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	copyCfg := *cfg
	copyCfg.LAPIKey, copyCfg.MachinePassword, copyCfg.MetricsToken = "", "", ""
	return &copyCfg, nil
}

// TestConnection probes candidate settings without saving anything. Locked fields use the environment.
func (m *crowdsecManager) TestConnection(ctx context.Context, in ConnectionSettings) (*ConnectionTest, error) {
	out := &ConnectionTest{LAPI: "not_configured", Machine: "not_configured", AppSec: "not_configured", Metrics: "not_configured", ObservedAt: time.Now().UTC().Format(time.RFC3339)}
	out.Engine = m.engineState(ctx)
	out.CanGenerateKey = m.canGenerateKey(ctx)
	cfg, err := m.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	lapiURL := strings.TrimSpace(in.LAPIURL)
	if m.defaultLAPIURL != "" || lapiURL == "" {
		lapiURL = cfg.LAPIURL
	}
	if lapiURL != "" {
		u, urlErr := registeredURL(lapiURL)
		if urlErr != nil {
			return nil, ErrCrowdSecUnreachable
		}
		lapiURL = u.String()
		out.LAPIURL = lapiURL
		probeCtx, cancel := context.WithTimeout(withLAPIURL(ctx, lapiURL), 8*time.Second)
		defer cancel()
		key := m.settings.APIKey
		if key == "" && newSecretInput(in.LAPIKey) {
			key = strings.TrimSpace(in.LAPIKey)
		}
		if key == "" {
			key = m.bouncerCredential(ctx)
		}
		certificate, certErr := m.hasClientCertificate(ctx, false)
		if certErr != nil {
			return nil, certErr
		}
		if key != "" || certificate {
			if certificate {
				key = ""
			}
			out.LAPI = probeState(m.verifyLAPIConnection(probeCtx, lapiURL, key))
		} else {
			out.LAPI = "unreachable"
			if m.lapiHealthy(probeCtx, lapiURL) {
				out.LAPI = "reachable"
			}
		}
		machineID, password, _ := m.machineCredentials(ctx)
		if m.settings.MachineID == "" && strings.TrimSpace(in.MachineID) != "" {
			machineID = strings.TrimSpace(in.MachineID)
		}
		if m.settings.MachinePassword == "" && newSecretInput(in.MachinePassword) {
			password = in.MachinePassword
		}
		if machineID != "" && password != "" {
			out.Machine = probeState(m.verifyMachineCredentials(probeCtx, machineID, password))
		}
	}
	appSecURL := m.settings.AppSecURL
	if appSecURL == "" {
		appSecURL = strings.TrimSpace(in.AppSecURL)
	}
	if appSecURL != "" {
		u, urlErr := registeredURL(appSecURL)
		if urlErr != nil {
			return nil, ErrCrowdSecUnreachable
		}
		out.AppSec = "unreachable"
		if m.endpointAnswers(ctx, u.String()+"/") {
			out.AppSec = "reachable"
		}
	}
	metricsURL, token := m.settings.MetricsURL, m.settings.MetricsToken
	if metricsURL == "" {
		metricsURL = strings.TrimSpace(in.MetricsURL)
	}
	if token == "" {
		if newSecretInput(in.MetricsToken) {
			token = in.MetricsToken
		} else if saved, decryptErr := m.secrets.DecryptSecret(ctx, crowdsecMetricsTokenSecret); decryptErr == nil {
			token = strings.TrimSpace(saved)
		}
	}
	if metricsURL != "" {
		if _, urlErr := registeredMetricsURL(metricsURL); urlErr != nil {
			return nil, ErrCrowdSecUnreachable
		}
		out.Metrics = m.metricsFrom(ctx, metricsURL, token, m.effectiveAccessLogSource(ctx)).State
	}
	return out, nil
}

func probeState(err error) string {
	switch {
	case err == nil:
		return "ok"
	case err == ErrCrowdSecInvalidKey:
		return "invalid_key"
	default:
		return "unreachable"
	}
}

// lapiHealthy is an unauthenticated reachability probe of the LAPI health endpoint.
func (m *crowdsecManager) lapiHealthy(ctx context.Context, lapiURL string) bool {
	return m.endpointAnswersStatus(ctx, lapiURL+"/health", true)
}

func (m *crowdsecManager) endpointAnswers(ctx context.Context, rawURL string) bool {
	return m.endpointAnswersStatus(ctx, rawURL, false)
}

// endpointAnswersStatus reports whether a pinned, redirect-free GET gets any HTTP answer (or 200 when strict).
func (m *crowdsecManager) endpointAnswersStatus(ctx context.Context, rawURL string, strict bool) bool {
	client := m.httpClient
	if client == nil {
		target, err := url.Parse(rawURL)
		if err != nil {
			return false
		}
		client, err = m.clientForURL(ctx, target.Scheme+"://"+target.Host, false)
		if err != nil {
			return false
		}
		defer client.CloseIdleConnections()
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", project.UserAgent())
	resp, err := copyClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return !strict || resp.StatusCode == http.StatusOK
}

// savedProtectedCIDRs reports whether the saved integration carries its own protected networks.
func (m *crowdsecManager) savedProtectedCIDRs(ctx context.Context) bool {
	row, err := m.store.GetItem(ctx, models.KindCrowdSec, "integration")
	if err != nil || row == nil {
		return false
	}
	var saved struct {
		ProtectedCIDRs []string `json:"protected_cidrs"`
	}
	return json.Unmarshal(row.Payload, &saved) == nil && len(saved.ProtectedCIDRs) > 0
}

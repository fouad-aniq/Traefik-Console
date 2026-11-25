// Package config loads application settings. It owns no live services, database,
// key material or filesystem mutations.
package config

import (
	"net"
	"time"
)

type Options struct {
	// Server & Database
	ConsoleHost       string
	ConsoleOrigin     string
	BindAddress       string
	Port              string
	DBPath            string
	JournalLimitBytes int64
	// SnapshotLimitBytes caps stored snapshot content (MAX_SNAPSHOT_BYTES).
	SnapshotLimitBytes int64
	// SnapshotRetentionAuto keeps this many automatic snapshots (SNAPSHOT_RETENTION_AUTO).
	SnapshotRetentionAuto int
	// OperationHistoryDays prunes older operations (OPERATION_HISTORY_DAYS).
	OperationHistoryDays int
	// AutoSnapshotBeforeStatic takes a pre_static snapshot before static changes (AUTO_SNAPSHOT_BEFORE_STATIC, default false).
	AutoSnapshotBeforeStatic bool

	// File System Paths
	TraefikStaticPath  string
	TraefikDynamicDir  string
	ACMEObservationDir string
	RuntimeACMEDir     string

	RuntimeSecretsDir string
	SecretsDir        string
	MasterKeyFile     string

	// Traefik Live API Observation
	TraefikAPIURL       string
	VerifyConvergenceTo time.Duration

	// Authentication: the separate auth database, cookie and session policy.
	Auth AuthOptions

	// Optional direct Docker endpoint; empty disables container operations.
	Docker       DockerOptions
	ProbeTargets map[string]string // e.g., {"websecure": "127.0.0.1:443"}

	// Optional CrowdSec integration. Connection and trust material are registered
	// by the operator; browsers can never replace these destinations or paths.
	ProtectedCIDRs []string
	CrowdSec       CrowdSecOptions
}

// AuthOptions configure sign-in. Credentials themselves live in AuthDBPath, never in the environment.
type AuthOptions struct {
	// DBPath is the private auth database (AUTH_DB_PATH, default auth.db beside DB_PATH).
	DBPath string
	// CookieSecure marks cookies Secure (COOKIE_SECURE; default true when CONSOLE_ORIGIN is https).
	CookieSecure bool
	// IdleTimeout ends unused sessions (SESSION_IDLE_MINUTES, default 120).
	IdleTimeout time.Duration
	// MaxLifetime ends every session after sign-in (SESSION_MAX_HOURS, default 24).
	MaxLifetime time.Duration
	// TrustedProxies whose X-Forwarded-For is believed for rate limiting (TRUSTED_PROXIES, CIDR list).
	TrustedProxies []*net.IPNet
}

type CrowdSecOptions struct {
	LAPIURL         string
	APIKey          string
	MachineID       string
	MachinePassword string
	CAFile          string
	ClientCertFile  string
	ClientKeyFile   string
	BouncerCertFile string
	BouncerKeyFile  string
	MachineCertFile string
	MachineKeyFile  string
	AppSecURL       string
	MetricsURL      string
	MetricsToken    string
	AccessLogSource string
	ReadTimeout     time.Duration
	AlertLimit      int
	ReadTimeoutSet  bool
	AlertLimitSet   bool
	// ProtectedCIDRsSet is true when CROWDSEC_PROTECTED_CIDRS was given; the Console then cannot change them.
	ProtectedCIDRsSet bool
	// Environment holds every CROWDSEC_* integration variable that was set, by name. Values are the raw
	// environment values (secrets included); callers must redact before presenting them.
	Environment map[string]string
}

// CrowdSecEnvironmentVariables are the integration variables an operator may set; when one is set the
// matching Console setting is read-only.
var CrowdSecEnvironmentVariables = []string{
	"CROWDSEC_LAPI_URL", "CROWDSEC_API_KEY", "CROWDSEC_MACHINE_ID", "CROWDSEC_MACHINE_PASSWORD",
	"CROWDSEC_CA_CERT", "CROWDSEC_CLIENT_CERT", "CROWDSEC_CLIENT_KEY", "CROWDSEC_BOUNCER_CERT", "CROWDSEC_BOUNCER_KEY",
	"CROWDSEC_MACHINE_CERT", "CROWDSEC_MACHINE_KEY", "CROWDSEC_APPSEC_URL", "CROWDSEC_METRICS_URL", "CROWDSEC_METRICS_TOKEN",
	"CROWDSEC_ACCESS_LOG_SOURCE", "CROWDSEC_READ_TIMEOUT", "CROWDSEC_ALERT_LIMIT", "CROWDSEC_PROTECTED_CIDRS",
	"CROWDSEC_CONTAINER",
}

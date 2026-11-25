package status

import (
	time "time"

	"github.com/traefik-console/internal/models"
)

type OverviewObservation struct {
	TraefikVersion      string    `json:"traefikVersion"`
	TraefikStatus       string    `json:"traefikStatus"` // ready, degraded, unavailable, unconfigured
	Connected           bool      `json:"connected"`
	RoutesLoaded        int       `json:"routesLoaded"`
	RoutesTotal         int       `json:"routesTotal"`
	CertificatesPartial bool      `json:"certificatesPartial"`
	CertsExpiring30d    int       `json:"certsExpiring30d"`
	CrowdSecStatus      string    `json:"crowdSecStatus"`
	CrowdSecDecisions   int       `json:"crowdSecDecisions"`
	ObservedAt          time.Time `json:"observedAt"`
	IsStale             bool      `json:"isStale"`
}

// VerificationOutcome captures attributable observation outcomes.
type VerificationOutcome struct {
	Observed         bool
	Loaded           bool
	AttributableErr  bool
	ErrorMessage     string
	UnrelatedBackend bool
}

// ServiceDiagnostic is a bounded snapshot of Traefik's existing runtime view.
// It never probes a request-supplied host, URL, port, command, or filesystem path.
type ServiceDiagnostic struct {
	Protocol         string                     `json:"protocol"`
	Name             string                     `json:"name"`
	Observed         bool                       `json:"observed"`
	RuntimeStatus    string                     `json:"runtimeStatus"`
	HealthStatus     string                     `json:"healthStatus"`           // healthy, unhealthy, mixed, unknown
	ServerStatus     map[string]int             `json:"serverStatus,omitempty"` // counts by status; backend URLs are never returned
	Routers          []ServiceRouterObservation `json:"routers"`
	RoutersTruncated bool                       `json:"routersTruncated,omitempty"`
	ObservedAt       time.Time                  `json:"observedAt"`
}

type ServiceRouterObservation struct {
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	EntryPoints []string `json:"entryPoints,omitempty"`
}

// CertificateInfo represents a public X.509 certificate parsed from acme.json.
// Private keys are never parsed or stored in memory beyond initial JSON decoding.
type CertificateInfo struct {
	ResolverName string    `json:"resolver_name"`
	MainDomain   string    `json:"main_domain"`
	SANs         []string  `json:"sans,omitempty"`
	Issuer       string    `json:"issuer"`
	SerialNumber string    `json:"serial_number"`
	Fingerprint  string    `json:"fingerprint"`
	NotBefore    time.Time `json:"not_before"`
	ExpiresAt    time.Time `json:"expires_at"`
	DaysLeft     int       `json:"days_left"`
	Status       string    `json:"status"` // "valid", "expiring_soon" (<=30d), "expired"
	// UsedBy is filled by the API at read time; absent when the domains or routers could not be read.
	UsedBy *models.CertificateUsage `json:"used_by,omitempty"`
}

// ExternalItem represents a resource discovered in Traefik (via Docker labels,
// static config, or external files) that is not managed in SQLite.
type ExternalItem struct {
	Name     string         `json:"name"`      // Bare name without @provider suffix
	FullName string         `json:"full_name"` // e.g., "my-app@docker"
	Provider string         `json:"provider"`  // "docker", "file", "internal", "static"
	Status   string         `json:"status"`    // "enabled", "error", "disabled"
	ErrorMsg string         `json:"error_msg,omitempty"`
	Raw      map[string]any `json:"raw"`
}

package crowdsec

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/docker"
	"github.com/traefik-console/internal/engine"
)

type crowdsecManager struct {
	protected      []*net.IPNet
	store          resourceStore
	secrets        secretPreparer
	decisions      docker.DecisionManager
	applier        engine.Applier
	defaultLAPIURL string
	settings       Settings
	httpClient     *http.Client
	machineClient  *http.Client
	metricsClient  *http.Client
	bouncerTLS     *tls.Config
	machineTLS     *tls.Config
}

// Settings is startup-owned integration configuration. Secret values are used
// only as fallbacks when no encrypted credential has been saved by the Console.
type Settings struct {
	APIKey          string
	MachineID       string
	MachinePassword string
	CAFile          string
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
	// ProtectedCIDRsSet makes the startup protected networks authoritative (CROWDSEC_PROTECTED_CIDRS).
	ProtectedCIDRsSet bool
	// Environment lists the integration variables the operator set. Secret values are never kept here.
	Environment []EnvVariable
	// Runtime describes the registered container (Docker host and CrowdSec container); startup-only.
	Runtime RuntimeRegistration
}

func NewCrowdSecManager(store resourceStore, secretsMgr secretPreparer, decisions docker.DecisionManager, defaultLAPIURL string, applier engine.Applier, protected []string) (CrowdSecManager, error) {
	return NewCrowdSecManagerWithOptions(store, secretsMgr, decisions, defaultLAPIURL, Settings{}, applier, protected)
}

func NewCrowdSecManagerWithOptions(store resourceStore, secretsMgr secretPreparer, decisions docker.DecisionManager, defaultLAPIURL string, settings Settings, applier engine.Applier, protected []string) (CrowdSecManager, error) {
	nets := []*net.IPNet{}
	for _, raw := range protected {
		_, n, e := net.ParseCIDR(strings.TrimSpace(raw))
		if e != nil {
			return nil, e
		}
		nets = append(nets, n)
	}
	if (settings.BouncerCertFile == "") != (settings.BouncerKeyFile == "") || (settings.MachineCertFile == "") != (settings.MachineKeyFile == "") {
		return nil, fmt.Errorf("crowdsec mTLS requires both certificate and private key for each role")
	}
	if settings.ReadTimeout <= 0 {
		settings.ReadTimeout = 20 * time.Second
	}
	if settings.ReadTimeout > 120*time.Second {
		settings.ReadTimeout = 120 * time.Second
	}
	if settings.AlertLimit <= 0 {
		settings.AlertLimit = 500
	}
	if settings.AlertLimit > 1000 {
		settings.AlertLimit = 1000
	}
	if defaultLAPIURL != "" {
		if _, err := registeredURL(defaultLAPIURL); err != nil {
			return nil, err
		}
	}
	if settings.AppSecURL != "" {
		if _, err := registeredURL(settings.AppSecURL); err != nil {
			return nil, err
		}
	}
	m := &crowdsecManager{protected: nets,
		store:          store,
		secrets:        secretsMgr,
		decisions:      decisions,
		applier:        applier,
		defaultLAPIURL: defaultLAPIURL,
		settings:       settings,
		httpClient:     nil,
	}
	if err := m.configureTransports(); err != nil {
		return nil, err
	}
	return m, nil
}

type resourceStore interface {
	db.ItemReader
	db.SecretReader
}

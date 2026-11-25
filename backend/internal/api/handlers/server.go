package handlers

import (
	"fmt"
	"net/http"

	"github.com/traefik-console/internal/api/middleware"
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
	"github.com/traefik-console/internal/traefik/status"
	"github.com/traefik-console/internal/updates"
)

// Dependencies names the API's collaborators without exposing file writes or a service locator.
// Interface values must contain valid implementations; construction does not use reflection.
type Dependencies struct {
	Web              http.Handler
	History          db.HistoryReader
	Observation      *status.ObservationClient
	Container        docker.TraefikContainerInspector
	Operations       engine.Operations
	ReadStaticConfig func() ([]byte, error)
	Resolvers        resolvers.ResolverManager
	EntryPoints      entrypoints.EntryPointManager
	Domains          domains.DomainManager
	Certificates     certificates.CertificateManager
	Routers          routers.RouterManager
	Services         services.ServiceManager
	Middlewares      middlewares.MiddlewareManager
	Plugins          plugins.PluginManager
	CrowdSec         crowdsec.CrowdSecManager
	// Nil deliberately reports unmapped ACME observation; it never enables an arbitrary file reader.
	ACMEObserver *status.ACMEObserver
	// Updates reports a newer Console release; nil when the check is off or the build is local.
	Updates *updates.Checker
}

type Server struct{ deps Dependencies }

func NewServer(deps Dependencies, policy middleware.Policy) (http.Handler, error) {
	required := []struct {
		name    string
		missing bool
	}{
		{"History", deps.History == nil}, {"Observation", deps.Observation == nil},
		{"Container", deps.Container == nil}, {"Operations", deps.Operations == nil},
		{"ReadStaticConfig", deps.ReadStaticConfig == nil}, {"Resolvers", deps.Resolvers == nil},
		{"EntryPoints", deps.EntryPoints == nil},
		{"Domains", deps.Domains == nil}, {"Certificates", deps.Certificates == nil},
		{"Routers", deps.Routers == nil}, {"Services", deps.Services == nil},
		{"Middlewares", deps.Middlewares == nil}, {"Plugins", deps.Plugins == nil}, {"CrowdSec", deps.CrowdSec == nil},
	}
	for _, entry := range required {
		if entry.missing {
			return nil, fmt.Errorf("API dependency %s is required", entry.name)
		}
	}
	s := &Server{deps: deps}
	return middleware.Chain(s.routes(), policy), nil
}

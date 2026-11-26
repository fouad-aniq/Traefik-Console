// Package consoleroute creates the Console's own service and route as managed resources, once.
//
// The shipped Compose bundles route the Console address through a bootstrap router in
// management.yml (outside the app, so the Console is reachable on its first start). Once Traefik
// reports it, the Console copies it into a managed service and route named "console", through the
// same managers as the Services and Routes pages: they are written to the YAML and the database
// and can then be edited like any other (middlewares, domain, certificate resolver...). The route
// gets a priority above the bootstrap router, which stays as a fallback.
package consoleroute

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/traefik-console/internal/models"
)

const (
	// Name of the managed service and route.
	Name = "console"
	// BootstrapName is the router and service in the shipped management.yml.
	BootstrapName = "console-bootstrap"
	// SettingKey remembers the seeding: a route the operator deleted or renamed is never recreated.
	SettingKey = "console_route_seeded"
)

type Settings interface {
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
}

type Services interface {
	ListServices(ctx context.Context) ([]models.UIResource[models.ServiceConfig], error)
	UpsertService(ctx context.Context, name string, input models.ServiceConfig, opts models.WriteOptions) (*models.WriteResult[models.ServiceConfig], error)
}

type Routers interface {
	ListRouters(ctx context.Context) ([]models.UIResource[models.RouterConfig], error)
	UpsertRouter(ctx context.Context, name string, input models.RouterConfig, opts models.WriteOptions) (*models.WriteResult[models.RouterConfig], error)
}

// ErrNotReady means Traefik has not reported the bootstrap service yet; try again later.
var ErrNotReady = errors.New("console route: bootstrap service not observed yet")

var hostRule = regexp.MustCompile("^Host\\(`([^`]+)`\\)$")

func same(name, want string) bool { return strings.TrimSuffix(name, "@file") == want }

// Seed creates the managed service and route when they are due. It returns done=true when there
// is nothing left to do on this start: seeded now or earlier, a "console" route already exists, or
// Traefik has no bootstrap router (the operator brings their own Traefik and route).
func Seed(ctx context.Context, settings Settings, services Services, routers Routers) (bool, error) {
	if v, err := settings.GetSetting(ctx, SettingKey); err != nil {
		return false, err
	} else if v != "" {
		return true, nil
	}
	allRouters, err := routers.ListRouters(ctx)
	if err != nil {
		return false, err
	}
	var bootstrap *models.UIResource[models.RouterConfig]
	for i, r := range allRouters {
		if same(r.Name, Name) {
			return true, settings.SetSetting(ctx, SettingKey, "existing")
		}
		if same(r.Name, BootstrapName) && r.Origin == "external" {
			bootstrap = &allRouters[i]
		}
	}
	if bootstrap == nil || bootstrap.Config.Rule == "" {
		return true, nil
	}
	allServices, err := services.ListServices(ctx)
	if err != nil {
		return false, err
	}
	var target, existing *models.UIResource[models.ServiceConfig]
	for i, s := range allServices {
		switch {
		case same(s.Name, Name):
			existing = &allServices[i]
		case same(s.Name, BootstrapName) && s.Origin == "external":
			target = &allServices[i]
		}
	}
	if existing == nil {
		if target == nil || len(target.Config.Servers) == 0 {
			return false, ErrNotReady
		}
		servers := make([]models.ServerEndpoint, 0, len(target.Config.Servers))
		for _, s := range target.Config.Servers {
			if s.URL != "" {
				servers = append(servers, models.ServerEndpoint{URL: s.URL})
			}
		}
		if len(servers) == 0 {
			return false, ErrNotReady
		}
		if _, err = services.UpsertService(ctx, Name, models.ServiceConfig{Servers: servers}, models.WriteOptions{}); err != nil {
			return false, err
		}
	}
	if _, err = routers.UpsertRouter(ctx, Name, routeFrom(bootstrap.Config), models.WriteOptions{}); err != nil {
		return false, err
	}
	return true, settings.SetSetting(ctx, SettingKey, "seeded")
}

// routeFrom copies the bootstrap router: same match, entry points and TLS, the managed service, and
// a priority above the bootstrap router (whose default priority is its rule length).
func routeFrom(b models.RouterConfig) models.RouterConfig {
	out := models.RouterConfig{
		EntryPoints: append([]string(nil), b.EntryPoints...),
		Service:     Name,
		Priority:    len(b.Rule) + 1,
	}
	if b.Priority >= out.Priority {
		out.Priority = b.Priority + 1
	}
	if m := hostRule.FindStringSubmatch(b.Rule); m != nil {
		out.Domains = []string{m[1]}
	} else {
		out.CustomRule = b.Rule
	}
	if b.TLS != nil {
		out.TLS = &models.RouterTLS{Enabled: true, CertResolver: b.TLS.CertResolver, Options: b.TLS.Options}
	}
	return out
}

// Run seeds in the background, retrying while Traefik is not ready or a write fails (for example
// while another operation runs), until done or ctx ends.
func Run(ctx context.Context, settings Settings, services Services, routers Routers, every time.Duration, attempts int, logf func(string, ...any)) {
	for i := 0; i < attempts; i++ {
		done, err := Seed(ctx, settings, services, routers)
		if done {
			if err != nil {
				logf("console route: %v", err)
			}
			return
		}
		if i == attempts-1 {
			logf("console route not created after %d attempts: %v", attempts, err)
			return
		}
		timer := time.NewTimer(every)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Package consoleroute creates the Console's own service and route as managed resources, once.
//
// The Console creates a service and a route named "console" through the same managers as the
// Services and Routes pages: they are written to the YAML and the database and can then be edited
// like any other (middlewares, domain, certificate resolver...).
//
//   - When Traefik reports the bootstrap router of the shipped bundles (management.yml), the route
//     copies it, with a priority above it; the bootstrap router stays as a fallback.
//   - Otherwise the route is built from the Console's settings: CONSOLE_HOST on the HTTPS entrypoint
//     found in Traefik's static configuration, to the Console at CONSOLE_UPSTREAM.
//
// Nothing is created when another route already serves CONSOLE_HOST.
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

type EntryPoints interface {
	ListEntryPoints(ctx context.Context) ([]models.UIResource[models.EntryPointConfig], error)
}

// Config comes from the Console's settings.
type Config struct {
	Host     string // CONSOLE_HOST (a port, if any, is dropped)
	Upstream string // how Traefik reaches the Console, e.g. http://console:8081
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
// is nothing left to do on this start: seeded now or earlier, a "console" route or another route
// for CONSOLE_HOST already exists, or there is nothing to build the route from.
func Seed(ctx context.Context, cfg Config, settings Settings, services Services, routers Routers, entryPoints EntryPoints) (bool, error) {
	if v, err := settings.GetSetting(ctx, SettingKey); err != nil {
		return false, err
	} else if v != "" {
		return true, nil
	}
	allRouters, err := routers.ListRouters(ctx)
	if err != nil {
		return false, err
	}
	host := hostOnly(cfg.Host)
	var bootstrap *models.UIResource[models.RouterConfig]
	servedElsewhere := false
	for i, r := range allRouters {
		if same(r.Name, Name) {
			return true, settings.SetSetting(ctx, SettingKey, "existing")
		}
		if same(r.Name, BootstrapName) && r.Origin == "external" {
			bootstrap = &allRouters[i]
		} else if host != "" && strings.Contains(r.Config.Rule, "`"+host+"`") {
			servedElsewhere = true
		}
	}
	var route models.RouterConfig
	var upstream []models.ServerEndpoint
	switch {
	case bootstrap != nil && bootstrap.Config.Rule != "":
		route = routeFrom(bootstrap.Config)
	case servedElsewhere:
		// The operator's own route (Docker labels, a file...) already serves the Console.
		return true, settings.SetSetting(ctx, SettingKey, "existing")
	case host == "" || cfg.Upstream == "":
		return true, nil
	default:
		eps, err := entryPoints.ListEntryPoints(ctx)
		if err != nil {
			return false, err
		}
		ep := httpsEntryPoint(eps)
		if ep == "" {
			return true, nil
		}
		route = models.RouterConfig{EntryPoints: []string{ep}, Domains: []string{host}, Service: Name, TLS: &models.RouterTLS{Enabled: true}}
		upstream = []models.ServerEndpoint{{URL: cfg.Upstream}}
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
		if upstream == nil {
			if target == nil {
				return false, ErrNotReady
			}
			for _, s := range target.Config.Servers {
				if s.URL != "" {
					upstream = append(upstream, models.ServerEndpoint{URL: s.URL})
				}
			}
			if len(upstream) == 0 {
				return false, ErrNotReady
			}
		}
		if _, err = services.UpsertService(ctx, Name, models.ServiceConfig{Servers: upstream}, models.WriteOptions{}); err != nil {
			return false, err
		}
	}
	if _, err = routers.UpsertRouter(ctx, Name, route, models.WriteOptions{}); err != nil {
		return false, err
	}
	return true, settings.SetSetting(ctx, SettingKey, "seeded")
}

func hostOnly(h string) string {
	h = strings.TrimSpace(h)
	if strings.Contains(h, ":") && !strings.HasPrefix(h, "[") {
		h, _, _ = strings.Cut(h, ":")
	}
	return h
}

// httpsEntryPoint picks the entrypoint web traffic with TLS arrives on: one that sets TLS for its
// routers, else one declared as https, else one on port 443 or 8443. Entrypoints that only redirect
// and UDP ones are skipped.
func httpsEntryPoint(eps []models.UIResource[models.EntryPointConfig]) string {
	best, bestScore := "", 0
	for _, e := range eps {
		c := e.Config
		if c.Protocol == "udp" || (c.HTTP != nil && c.HTTP.Redirect != nil) {
			continue
		}
		score := 0
		switch {
		case c.HTTP != nil && c.HTTP.TLS != nil:
			score = 3
		case c.Kind == "https":
			score = 2
		case c.Port == 443 || c.Port == 8443:
			score = 1
		}
		name := c.Name
		if name == "" {
			name = e.Name
		}
		if score > bestScore || (score == bestScore && score > 0 && name < best) {
			best, bestScore = name, score
		}
	}
	return best
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
func Run(ctx context.Context, cfg Config, settings Settings, services Services, routers Routers, entryPoints EntryPoints, every time.Duration, attempts int, logf func(string, ...any)) {
	for i := 0; i < attempts; i++ {
		done, err := Seed(ctx, cfg, settings, services, routers, entryPoints)
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

package middlewares

import (
	"sort"
	"strings"

	"github.com/traefik-console/internal/models"
)

// refersTo matches a reference the way Traefik resolves it from the file provider: a bare name and
// name@file are the same middleware; any other provider suffix is part of the name.
func refersTo(refs []string, name string) bool {
	for _, r := range refs {
		if strings.TrimSuffix(strings.TrimSpace(r), "@file") == name {
			return true
		}
	}
	return false
}

// Usage lists the routers and the other chains that reference a middleware or chain name. It uses
// the same rule as the delete guard, so "Used by" and the 409 on delete always agree.
func Usage(name string, routers []models.UIResource[models.RouterConfig], chains []models.UIResource[models.ChainConfig]) *models.MiddlewareUsage {
	return UsageOf(name, models.ServiceHTTP, routers, chains)
}

// UsageOf is Usage for a middleware of the given protocol: HTTP and TCP middlewares are separate
// pools, so only routers of that protocol count, and only HTTP middlewares can be chain members.
func UsageOf(name, protocol string, routers []models.UIResource[models.RouterConfig], chains []models.UIResource[models.ChainConfig]) *models.MiddlewareUsage {
	u := &models.MiddlewareUsage{Routers: []models.RouterRef{}, Chains: []models.ChainRef{}}
	for _, r := range routers {
		if models.RouterProtocol(r.Config) == protocol && refersTo(r.Config.Middlewares, name) {
			u.Routers = append(u.Routers, models.RouterRefOf(r))
		}
	}
	if protocol != models.ServiceHTTP {
		return u
	}
	for _, c := range chains {
		if c.Name != name && refersTo(c.Config.Middlewares, name) {
			members := c.Config.Middlewares
			if members == nil {
				members = []string{}
			}
			u.Chains = append(u.Chains, models.ChainRef{Name: c.Name, Members: members, ReadOnly: c.ReadOnly})
		}
	}
	sort.Slice(u.Routers, func(i, j int) bool { return u.Routers[i].Name < u.Routers[j].Name })
	sort.Slice(u.Chains, func(i, j int) bool { return u.Chains[i].Name < u.Chains[j].Name })
	return u
}

package services

import (
	"sort"
	"strings"

	"github.com/traefik-console/internal/models"
)

// qualified is the full name Traefik gives a service reference: a bare name belongs to the
// provider that declares the referencing router (name@file for a managed router).
func qualified(ref, provider string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.Contains(ref, "@") {
		return ref
	}
	if provider == "" {
		provider = "file"
	}
	return ref + "@" + provider
}

// serviceKey is the runtime name of a listed service: managed services are name@file, external
// ones are already listed under their qualified name.
func serviceKey(svc models.UIResource[models.ServiceConfig]) string {
	if svc.Origin == "managed" {
		return svc.Name + "@file"
	}
	return qualified(svc.Name, svc.Provider)
}

// Usage lists the routers that send traffic to a service, resolving each router's reference the
// way Traefik does (relative to the router's own provider).
func Usage(svc models.UIResource[models.ServiceConfig], routers []models.UIResource[models.RouterConfig]) *models.ServiceUsage {
	key := serviceKey(svc)
	u := &models.ServiceUsage{Routers: []models.RouterRef{}}
	for _, r := range routers {
		if r.Config.Service != "" && qualified(r.Config.Service, r.Provider) == key {
			u.Routers = append(u.Routers, models.RouterRefOf(r))
		}
	}
	sort.Slice(u.Routers, func(i, j int) bool { return u.Routers[i].Name < u.Routers[j].Name })
	return u
}

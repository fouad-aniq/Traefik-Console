package status

import (
	"context"
	"time"
)

// GetOverview returns polled telemetry. If the API is unreachable, it explicitly
// reports TraefikStatus: 'unavailable' and Connected: false with the current timestamp.
func (oc *ObservationClient) GetOverview(ctx context.Context) OverviewObservation {
	now := time.Now().UTC()
	data, err := oc.FetchRawData(ctx)
	if err != nil {
		return OverviewObservation{
			TraefikVersion:    "unknown",
			TraefikStatus:     "unavailable",
			Connected:         false,
			RoutesLoaded:      0,
			RoutesTotal:       0,
			CertsExpiring30d:  0,
			CrowdSecStatus:    "unconfigured",
			CrowdSecDecisions: 0,
			ObservedAt:        now,
			IsStale:           true,
		}
	}

	routersLoaded := 0
	routersTotal := 0
	for _, proto := range []string{"http", "tcp", "udp"} {
		routers := extractCategoryMap(data, proto, "routers")
		routersTotal += len(routers)
		for _, r := range routers {
			if m, ok := r.(map[string]any); ok && m["status"] == "enabled" {
				routersLoaded++
			}
		}
	}
	version := "unknown" // rawdata does not report the running Traefik version.

	status := "ready"
	if routersTotal > 0 && routersLoaded < routersTotal {
		status = "degraded"
	}

	return OverviewObservation{
		TraefikVersion:    version,
		TraefikStatus:     status,
		Connected:         true,
		RoutesLoaded:      routersLoaded,
		RoutesTotal:       routersTotal,
		CertsExpiring30d:  0,
		CrowdSecStatus:    "unconfigured",
		CrowdSecDecisions: 0,
		ObservedAt:        now,
		IsStale:           false,
	}
}

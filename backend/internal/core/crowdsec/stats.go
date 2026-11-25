package crowdsec

import (
	"context"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/traefik-console/internal/docker"
)

const maxStatsAlerts = 400

func (m *crowdsecManager) alertSample(ctx context.Context, windowHours, maximum int) ([]docker.Alert, bool, error) {
	items := make([]docker.Alert, 0, maximum)
	for page := 0; len(items) < maximum && page < 10; page++ {
		result, err := m.ListAlerts(ctx, AlertQuery{Page: page, Limit: 100, SinceHours: windowHours})
		if err != nil {
			return nil, false, err
		}
		remaining := min(100, maximum-len(items))
		if len(result.Items) > remaining {
			result.Items = result.Items[:remaining]
		}
		items = append(items, result.Items...)
		if !result.HasMore {
			return items, false, nil
		}
	}
	return items, true, nil
}

// CountItem is one bar of a ranking.
type CountItem struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type TimelinePoint struct {
	Start string `json:"start"`
	Count int    `json:"count"`
}

// CrowdSecStats summarises what CrowdSec itself recorded. It never counts HTTP requests: CrowdSec does not
// see visits, and the Traefik bouncer plugin reports no per-request counters, so "visits" and "blocked
// requests" are not available here. Alerts are detected attacks; decisions are the resulting bans.
type CrowdSecStats struct {
	Available       bool             `json:"available"`
	WindowHours     int              `json:"window_hours"`
	GeneratedAt     string           `json:"generated_at"`
	Alerts          int              `json:"alerts"`
	Truncated       bool             `json:"truncated"`
	ActiveDecisions int              `json:"active_decisions"`
	DecisionsByType []CountItem      `json:"decisions_by_type"`
	DecisionsByOrig []CountItem      `json:"decisions_by_origin"`
	TopCountries    []CountItem      `json:"top_countries"`
	TopScenarios    []CountItem      `json:"top_scenarios"`
	TopSources      []CountItem      `json:"top_sources"`
	Timeline        []TimelinePoint  `json:"timeline"`
	BucketHours     int              `json:"bucket_hours"`
	Bouncers        []docker.Bouncer `json:"bouncers"`
	Metrics         *MetricsSnapshot `json:"metrics,omitempty"`
	// Partial lists the sections that could not be read (alerts, decisions, bouncers).
	Partial []string `json:"partial"`
}

func ranking(counts map[string]int, limit int) []CountItem {
	items := make([]CountItem, 0, len(counts))
	for key, count := range counts {
		items = append(items, CountItem{Key: key, Count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Key < items[j].Key
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

func parseAlertTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// summarizeAlerts is pure so the aggregation is testable without a container.
func summarizeAlerts(alerts []docker.Alert, now time.Time, windowHours int) (countries, scenarios, sources []CountItem, timeline []TimelinePoint, bucketHours int) {
	bucketHours = 1
	if windowHours > 48 {
		bucketHours = 24
	}
	bucket := time.Duration(bucketHours) * time.Hour
	end := now.UTC().Truncate(bucket).Add(bucket)
	buckets := windowHours / bucketHours
	if windowHours%bucketHours != 0 {
		buckets++
	}
	start := end.Add(-time.Duration(buckets) * bucket)
	timeline = make([]TimelinePoint, buckets)
	for i := range timeline {
		timeline[i].Start = start.Add(time.Duration(i) * bucket).Format(time.RFC3339)
	}
	byCountry, byScenario, bySource := map[string]int{}, map[string]int{}, map[string]int{}
	for _, a := range alerts {
		country := alertCountry(a)
		byCountry[country]++
		if a.Reason != "" {
			byScenario[a.Reason]++
		}
		if a.Value != "" {
			bySource[a.Value]++
		}
		if at, ok := parseAlertTime(a.CreatedAt); ok && !at.Before(start) && at.Before(end) {
			timeline[int(at.Sub(start)/bucket)].Count++
		}
	}
	return ranking(byCountry, 60), ranking(byScenario, 10), ranking(bySource, 10), timeline, bucketHours
}

// Stats reads alerts, decisions and bouncers through the registered CrowdSec container. Each section fails
// independently and is reported in Partial instead of hiding the others.
func (m *crowdsecManager) Stats(ctx context.Context, windowHours int) (*CrowdSecStats, error) {
	if windowHours != 1 && windowHours != 24 && windowHours != 168 && windowHours != 720 {
		return nil, ErrCrowdSecInvalidKey
	}
	now := time.Now().UTC()
	out := &CrowdSecStats{WindowHours: windowHours, GeneratedAt: now.Format(time.RFC3339), Partial: []string{}, Bouncers: []docker.Bouncer{},
		DecisionsByType: []CountItem{}, DecisionsByOrig: []CountItem{}, TopCountries: []CountItem{}, TopScenarios: []CountItem{}, TopSources: []CountItem{}, Timeline: []TimelinePoint{}}
	if alerts, truncated, e := m.alertSample(ctx, windowHours, maxStatsAlerts); e != nil {
		out.Partial = append(out.Partial, "alerts")
	} else {
		out.Available = true
		out.Alerts = len(alerts)
		out.Truncated = truncated || len(alerts) >= maxStatsAlerts
		out.TopCountries, out.TopScenarios, out.TopSources, out.Timeline, out.BucketHours = summarizeAlerts(alerts, now, windowHours)
	}
	if decisions, e := m.ListDecisions(ctx); e != nil {
		out.Partial = append(out.Partial, "decisions")
	} else {
		out.Available = true
		byType, byOrigin := map[string]int{}, map[string]int{}
		for _, d := range decisions {
			byType[d.Type]++
			byOrigin[d.Origin]++
		}
		out.ActiveDecisions = len(decisions)
		out.DecisionsByType, out.DecisionsByOrig = ranking(byType, 0), ranking(byOrigin, 0)
	}
	// Bouncers are listed through the registered container only; without one there is nothing to report.
	if inspector, ok := m.decisions.(docker.CrowdSecInspector); ok && m.engineState(ctx) == "running" {
		if bouncers, e := inspector.ListBouncers(ctx); e != nil {
			out.Partial = append(out.Partial, "bouncers")
		} else {
			out.Available = true
			out.Bouncers = bouncers
		}
	}
	if metricsURL, _ := m.effectiveMetrics(ctx); metricsURL != "" {
		out.Metrics = m.Metrics(ctx)
		switch out.Metrics.State {
		case "available":
			out.Available = true
		case "unavailable":
			out.Partial = append(out.Partial, "metrics")
		}
	}
	return out, nil
}

// alertCountry is the alert source country as CrowdSec's GeoIP enrichment recorded it. Private, loopback
// and link-local sources never have one: they are grouped as LOCAL instead of being counted as unknown.
func alertCountry(a docker.Alert) string {
	if country := strings.ToUpper(strings.TrimSpace(a.Country)); country != "" {
		return country
	}
	ip := net.ParseIP(strings.TrimSpace(a.Value))
	if ip == nil {
		if _, network, err := net.ParseCIDR(strings.TrimSpace(a.Value)); err == nil {
			ip = network.IP
		}
	}
	if ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
		return "LOCAL"
	}
	return "??"
}

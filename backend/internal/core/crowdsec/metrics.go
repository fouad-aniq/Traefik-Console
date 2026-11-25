package crowdsec

import (
	"bufio"
	"context"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxMetricsResponseBytes = 256 * 1024

type MetricsSnapshot struct {
	State                 string  `json:"state"`
	ObservedAt            string  `json:"observed_at"`
	IngestionState        string  `json:"ingestion_state"`
	ParsedAccessLogEvents *uint64 `json:"parsed_access_log_events"`
	LAPIRequests          *uint64 `json:"lapi_requests"`
	BouncerPulls          *uint64 `json:"bouncer_pulls"`
	AppSecProcessed       *uint64 `json:"appsec_processed"`
	AppSecBlocked         *uint64 `json:"appsec_blocked"`
	CAPILastPull          string  `json:"capi_last_pull,omitempty"`
}

func registeredMetricsURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, ErrCrowdSecUnreachable
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "" {
		u.Path = "/metrics"
	}
	if u.Path != "/metrics" {
		return nil, ErrCrowdSecUnreachable
	}
	return u, nil
}

func (m *crowdsecManager) Metrics(ctx context.Context) *MetricsSnapshot {
	metricsURL, token := m.effectiveMetrics(ctx)
	return m.metricsFrom(ctx, metricsURL, token, m.effectiveAccessLogSource(ctx))
}

// metricsFrom reads one Prometheus endpoint; the URL, token and access log source are already resolved.
func (m *crowdsecManager) metricsFrom(ctx context.Context, metricsURL, token, accessLogSource string) *MetricsSnapshot {
	result := &MetricsSnapshot{State: "unavailable", IngestionState: "unverified", ObservedAt: time.Now().UTC().Format(time.RFC3339)}
	if metricsURL == "" {
		return result
	}
	u, err := registeredMetricsURL(metricsURL)
	if err != nil {
		return result
	}
	client := m.metricsClient
	if client == nil {
		client, err = pinnedClientWithTLS(ctx, u, m.bouncerTLS, m.settings.ReadTimeout)
		if err != nil {
			return result
		}
		defer client.CloseIdleConnections()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return result
	}
	req.Header.Set("Accept", "text/plain")
	req.Header.Set("User-Agent", "Traefik-Console/2.0")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return result
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		result.State = "authentication_rejected"
		return result
	}
	if resp.StatusCode != http.StatusOK {
		return result
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMetricsResponseBytes+1))
	if err != nil || len(data) > maxMetricsResponseBytes {
		return result
	}
	values := map[string]float64{}
	accessSource := map[string]float64{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 1024), 64*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		separator := strings.LastIndexByte(line, ' ')
		if separator <= 0 {
			return result
		}
		name := line[:separator]
		value, err := strconv.ParseFloat(strings.TrimSpace(line[separator+1:]), 64)
		if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) || value > 1<<53 {
			return result
		}
		metric := name
		if brace := strings.IndexByte(metric, '{'); brace >= 0 {
			metric = metric[:brace]
			if accessLogSource != "" && metric == "cs_filesource_hits_total" && strings.Contains(name, `source="`+accessLogSource+`"`) {
				accessSource[metric] += value
			}
		}
		values[metric] += value
	}
	if scanner.Err() != nil {
		return result
	}
	result.State = "available"
	result.ObservedAt = time.Now().UTC().Format(time.RFC3339)
	result.LAPIRequests = metricUint(values["cs_lapi_route_requests_total"])
	result.BouncerPulls = metricUint(values["cs_lapi_bouncer_requests_total"])
	result.AppSecProcessed = metricUint(values["cs_appsec_reqs_total"])
	result.AppSecBlocked = metricUint(values["cs_appsec_block_total"])
	if accessLogSource != "" {
		result.ParsedAccessLogEvents = metricUint(accessSource["cs_filesource_hits_total"])
		if *result.ParsedAccessLogEvents > 0 {
			result.IngestionState = "records_observed"
		} else {
			result.IngestionState = "no_records_observed"
		}
	}
	if timestamp := values["cs_capi_last_pull_timestamp"]; timestamp > 0 {
		result.CAPILastPull = time.Unix(int64(timestamp), 0).UTC().Format(time.RFC3339)
	}
	return result
}

func metricUint(value float64) *uint64 {
	converted := uint64(value)
	return &converted
}

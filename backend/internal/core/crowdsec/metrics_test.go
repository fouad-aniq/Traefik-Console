package crowdsec

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMetricsUsesOnlyRegisteredSourceAndNativeCounters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" || r.Header.Get("Authorization") != "Bearer metrics-secret" {
			t.Error("invalid metrics request")
		}
		fmt.Fprint(w, `
# TYPE cs_filesource_hits_total counter
cs_filesource_hits_total{source="/var/log/traefik/access.log",type="file"} 42
cs_filesource_hits_total{source="/var/log/other.log",type="file"} 9000
cs_lapi_route_requests_total{route="/v1/decisions",method="GET"} 12
cs_lapi_bouncer_requests_total{bouncer="traefik",route="/v1/decisions",method="GET"} 8
cs_appsec_reqs_total{source="appsec",appsec_engine="0.0.0.0:7422/"} 20
cs_appsec_block_total{source="appsec",appsec_engine="0.0.0.0:7422/"} 3
cs_capi_last_pull_timestamp 1762850000
`)
	}))
	defer server.Close()
	m := &crowdsecManager{metricsClient: server.Client(), settings: Settings{MetricsURL: server.URL, MetricsToken: "metrics-secret", AccessLogSource: "/var/log/traefik/access.log"}}
	metrics := m.Metrics(context.Background())
	if metrics.State != "available" || metrics.IngestionState != "records_observed" || metrics.ParsedAccessLogEvents == nil || *metrics.ParsedAccessLogEvents != 42 {
		t.Fatalf("ingestion was not measured against the registered source: %+v", metrics)
	}
	if *metrics.LAPIRequests != 12 || *metrics.BouncerPulls != 8 || *metrics.AppSecProcessed != 20 || *metrics.AppSecBlocked != 3 || metrics.CAPILastPull == "" {
		t.Fatalf("native metrics not projected: %+v", metrics)
	}
	m.settings.AccessLogSource = ""
	metrics = m.Metrics(context.Background())
	if metrics.ParsedAccessLogEvents != nil || metrics.IngestionState != "unverified" {
		t.Fatalf("unregistered log source became ingestion proof: %+v", metrics)
	}
}

func TestMetricsAndHubRejectMalformedResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "cs_appsec_reqs_total not-a-number") }))
	defer server.Close()
	m := &crowdsecManager{metricsClient: server.Client(), settings: Settings{MetricsURL: server.URL}}
	if got := m.Metrics(context.Background()); got.State != "unavailable" {
		t.Fatalf("malformed metrics accepted: %+v", got)
	}
	if _, err := registeredMetricsURL("http://crowdsec:6060/other"); err == nil {
		t.Fatal("unregistered metrics path accepted")
	}
}

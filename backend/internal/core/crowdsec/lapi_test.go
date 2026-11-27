package crowdsec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/traefik-console/internal/docker"
)

func TestNativeObservationSeparatesMachineAndBouncerCredentials(t *testing.T) {
	deleted := int64(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/watchers/login":
			var input struct {
				ID       string `json:"machine_id"`
				Password string `json:"password"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.ID != "machine" || input.Password != "machine-secret" {
				t.Error("invalid machine login")
			}
			if r.Header.Get("X-Api-Key") != "" {
				t.Error("bouncer key leaked into machine login")
			}
			fmt.Fprint(w, `{"token":"jwt-secret"}`)
		case "/v1/alerts":
			if r.Header.Get("Authorization") != "Bearer jwt-secret" || r.Header.Get("X-Api-Key") != "" {
				t.Error("alert read did not use only machine authentication")
			}
			if r.URL.Query().Get("has_active_decision") == "true" {
				fmt.Fprint(w, `[{"decisions":[{"id":9,"origin":"crowdsec","scenario":"test/probe","scope":"Ip","type":"ban","value":"192.0.2.9","duration":"1h"}]}]`)
				return
			}
			if (r.URL.Query().Get("limit") != "3" && r.URL.Query().Get("limit") != "4" && r.URL.Query().Get("limit") != "5") || r.URL.Query().Get("since") != "24h" || r.URL.Query().Get("scenario") != "test/probe" {
				t.Errorf("unexpected alert query: %s", r.URL.RawQuery)
			}
			fmt.Fprint(w, `[
				{"id":1,"scenario":"test/probe","kind":"waf","created_at":"2026-11-10T10:00:00Z","events_count":2,"source":{"scope":"Ip","value":"192.0.2.1","cn":"FR","as_name":"Example","as_number":"64500"}},
				{"id":2,"scenario":"test/probe","created_at":"2026-11-10T11:00:00Z","events_count":3,"source":{"scope":"Ip","value":"192.0.2.2"}},
				{"id":3,"scenario":"test/probe","created_at":"2026-11-10T12:00:00Z","events_count":4,"source":{"scope":"Ip","value":"192.0.2.3"}},
				{"id":4,"scenario":"test/probe","created_at":"2026-11-10T13:00:00Z","events_count":5,"source":{"scope":"Ip","value":"192.0.2.4"}},
				{"id":5,"scenario":"test/probe","created_at":"2026-11-10T14:00:00Z","events_count":6,"source":{"scope":"Ip","value":"192.0.2.5"}}
			]`)
		case "/v1/alerts/2":
			fmt.Fprint(w, `{"id":2,"scenario":"test/probe","created_at":"2026-11-10T11:00:00Z","events_count":3,"source":{"scope":"Ip","value":"192.0.2.2"},"decisions":[{"id":9,"value":"192.0.2.2","duration":"1h"}]}`)
		case "/v1/decisions":
			if r.Header.Get("X-Api-Key") != "bouncer-secret" || r.Header.Get("Authorization") != "" {
				t.Error("decision read did not use only bouncer authentication")
			}
			fmt.Fprint(w, `[{"id":9,"origin":"crowdsec","scenario":"test/probe","scope":"Ip","type":"ban","value":"192.0.2.9","duration":"1h"}]`)
		case "/v1/decisions/9":
			if r.Method != http.MethodDelete || r.Header.Get("Authorization") != "Bearer jwt-secret" {
				t.Error("exact decision deletion did not use machine authentication")
			}
			deleted = 9
			fmt.Fprint(w, `{"nbDeleted":"1"}`)
		case "/v1/allowlists":
			fmt.Fprint(w, `[{"name":"traefik-console-safe","description":"safe","items":[{"value":"192.0.2.0/24","description":"lab"}],"console_managed":false}]`)
		case "/v1/allowlists/check":
			var input struct {
				Targets []string `json:"targets"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Targets) != 1 || input.Targets[0] != "192.0.2.0/24" {
				t.Error("invalid allowlist check")
			}
			fmt.Fprint(w, `{"results":[{"target":"192.0.2.0/24","allowlists":["traefik-console-safe"]}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	m := &crowdsecManager{
		store: noIntegration{}, secrets: noSecret{}, defaultLAPIURL: server.URL,
		settings:   Settings{MachineID: "machine", MachinePassword: "machine-secret", APIKey: "bouncer-secret", AlertLimit: 500},
		httpClient: server.Client(), machineClient: server.Client(),
	}
	page, err := m.ListAlerts(context.Background(), AlertQuery{Page: 1, Limit: 2, SinceHours: 24, Scenario: "test/probe"})
	if err != nil || len(page.Items) != 2 || !page.HasMore || page.Items[0].ID != 3 || page.Items[0].EventsCount != 4 {
		t.Fatalf("native pagination failed: %+v %v", page, err)
	}
	first, err := m.ListAlerts(context.Background(), AlertQuery{Limit: 2, SinceHours: 24, Scenario: "test/probe"})
	if err != nil || len(first.Items) != 2 || first.Items[0].Country != "FR" || first.Items[0].ASNumber != "64500" || first.Items[0].Kind != "waf" {
		t.Fatalf("native alert projection failed: %+v %v", first, err)
	}
	detail, err := m.GetAlert(context.Background(), 2)
	if err != nil || len(detail.Decisions) != 1 || detail.Decisions[0].ID != 9 {
		t.Fatalf("alert detail failed: %+v %v", detail, err)
	}
	decisions, err := m.ListDecisions(context.Background())
	if err != nil || len(decisions) != 1 || decisions[0].ID != 9 {
		t.Fatalf("bouncer decisions failed: %v %v", decisions, err)
	}
	if err := m.DeleteDecisionByID(context.Background(), 9); err != nil || deleted != 9 {
		t.Fatalf("exact deletion failed: %d %v", deleted, err)
	}
	lists, err := m.ListAllowlists(context.Background())
	if err != nil || len(lists) != 1 || len(lists[0].Items) != 1 {
		t.Fatalf("allowlists failed: %+v %v", lists, err)
	}
	allowed, reason, err := m.CheckAllowlist(context.Background(), "192.0.2.0/24")
	if err != nil || !allowed || !strings.Contains(reason, "traefik-console-safe") {
		t.Fatalf("allowlist check failed: %t %q %v", allowed, reason, err)
	}
	serialized, _ := json.Marshal([]any{page, detail, decisions, lists})
	if strings.Contains(string(serialized), "machine-secret") || strings.Contains(string(serialized), "bouncer-secret") || strings.Contains(string(serialized), "jwt-secret") {
		t.Fatal("credential material was exposed by a public DTO")
	}
}

type fallbackInspector struct{ alerts []docker.Alert }

func (f fallbackInspector) ListDecisions(context.Context) ([]docker.Decision, error)  { return nil, nil }
func (f fallbackInspector) AddDecision(context.Context, string, string, string) error { return nil }
func (f fallbackInspector) DeleteDecision(context.Context, string) error              { return nil }
func (f fallbackInspector) DeleteDecisionByID(context.Context, int64) error           { return nil }
func (f fallbackInspector) CrowdSecEngine(context.Context) (*docker.EngineState, error) {
	return &docker.EngineState{Configured: true, Running: true}, nil
}
func (f fallbackInspector) ListAlerts(context.Context, int, int) ([]docker.Alert, error) {
	return f.alerts, nil
}
func (f fallbackInspector) InspectAlert(context.Context, int64) (*docker.Alert, error) {
	return nil, nil
}
func (f fallbackInspector) ListBouncers(context.Context) ([]docker.Bouncer, error) { return nil, nil }

func TestContainerFallbackFiltersBeforePagination(t *testing.T) {
	m := &crowdsecManager{store: noIntegration{}, secrets: noSecret{}, decisions: fallbackInspector{alerts: []docker.Alert{
		{ID: 1, Reason: "other", Origin: "crowdsec"}, {ID: 2, Reason: "wanted", Origin: "crowdsec"}, {ID: 3, Reason: "wanted", Origin: "crowdsec"}, {ID: 4, Reason: "wanted", Origin: "cscli"},
	}}}
	page, err := m.ListAlerts(context.Background(), AlertQuery{Limit: 1, Scenario: "wanted", Origin: "crowdsec"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != 2 || !page.HasMore {
		t.Fatalf("unexpected filtered first page: %+v %v", page, err)
	}
	page, err = m.ListAlerts(context.Background(), AlertQuery{Page: 1, Limit: 1, Scenario: "wanted", Origin: "crowdsec"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != 3 || page.HasMore {
		t.Fatalf("unexpected filtered last page: %+v %v", page, err)
	}
}

type pagingInspector struct {
	fallbackInspector
	limits []int
}

func (p *pagingInspector) ListAlerts(_ context.Context, _ int, limit int) ([]docker.Alert, error) {
	p.limits = append(p.limits, limit)
	return p.alerts[:min(limit, len(p.alerts))], nil
}

func TestStatsSampleUsesBoundedPages(t *testing.T) {
	inspector := &pagingInspector{fallbackInspector: fallbackInspector{alerts: make([]docker.Alert, 250)}}
	m := &crowdsecManager{store: noIntegration{}, secrets: noSecret{}, decisions: inspector}
	items, truncated, err := m.alertSample(context.Background(), 24, 250)
	if err != nil || truncated || len(items) != 250 {
		t.Fatalf("unexpected stats sample: %d %t %v", len(items), truncated, err)
	}
	if len(inspector.limits) != 3 || inspector.limits[0] != 101 || inspector.limits[1] != 201 || inspector.limits[2] != 301 {
		t.Fatalf("unexpected page sizes: %v", inspector.limits)
	}
}

func TestNativeObservationRejectsInvalidInputAndResponses(t *testing.T) {
	m := &crowdsecManager{store: noIntegration{}, secrets: noSecret{}, settings: Settings{AlertLimit: 500}}
	for _, query := range []AlertQuery{{Page: 10, Limit: 1, SinceHours: 24}, {Limit: 101, SinceHours: 24}, {Limit: 1, SinceHours: 721}, {Limit: 1, SinceHours: 24, Source: "not-an-ip"}, {Limit: 1, SinceHours: 24, Scenario: "bad\nvalue"}} {
		if _, err := m.ListAlerts(context.Background(), query); err == nil {
			t.Fatalf("invalid query accepted: %+v", query)
		}
	}
	if err := m.DeleteDecisionByID(context.Background(), 0); err == nil {
		t.Fatal("zero decision ID accepted")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/watchers/login" {
			fmt.Fprint(w, `{"token":"jwt-secret"}`)
			return
		}
		fmt.Fprint(w, `[] trailing`)
	}))
	defer server.Close()
	m.defaultLAPIURL = server.URL
	m.settings.MachineID, m.settings.MachinePassword = "machine", "machine-secret"
	m.machineClient = server.Client()
	if _, err := m.ListAlerts(context.Background(), AlertQuery{Limit: 1, SinceHours: 24}); err == nil {
		t.Fatal("invalid native JSON accepted")
	}
}

// capiLAPI answers like a LAPI with community-blocklist pulls: without include_capi=false the alerts
// include a CAPI "update" alert embedding more decisions than Console reads (the LAPI default).
func capiLAPI(t *testing.T, recorded *[]string) *httptest.Server {
	t.Helper()
	var huge strings.Builder
	huge.WriteString(`[{"id":90,"scenario":"update : +14995/-0 IPs","origin":"CAPI","created_at":"2026-11-27T10:00:00Z","events_count":0,"source":{"scope":"crowdsecurity/community-blocklist"},"decisions":[`)
	for i := 0; huge.Len() <= maxLAPIResponseBytes; i++ {
		if i > 0 {
			huge.WriteString(",")
		}
		fmt.Fprintf(&huge, `{"id":%d,"origin":"CAPI","scenario":"crowdsecurity/http-probing","scope":"Ip","type":"ban","value":"198.51.%d.%d","duration":"23h"}`, 1000+i, (i/250)%250, i%250)
	}
	huge.WriteString(`]}]`)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/watchers/login":
			fmt.Fprint(w, `{"token":"jwt-secret"}`)
		case "/v1/alerts":
			*recorded = append(*recorded, r.URL.Query().Get("include_capi"))
			if r.URL.Query().Get("include_capi") != "false" {
				fmt.Fprint(w, huge.String())
				return
			}
			if r.URL.Query().Get("has_active_decision") == "true" {
				fmt.Fprint(w, `[{"decisions":[{"id":7,"origin":"crowdsec","scenario":"crowdsecurity/http-probing","scope":"Ip","type":"ban","value":"192.0.2.7","duration":"3h"}]}]`)
				return
			}
			fmt.Fprint(w, `[{"id":7,"scenario":"crowdsecurity/http-probing","origin":"crowdsec","created_at":"2026-11-27T09:00:00Z","events_count":12,"source":{"scope":"Ip","value":"192.0.2.7","cn":"NL"}}]`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestAlertsLeaveOutBlocklistAlertsUnlessFilteredOn(t *testing.T) {
	var recorded []string
	server := capiLAPI(t, &recorded)
	defer server.Close()
	m := &crowdsecManager{
		store: noIntegration{}, secrets: noSecret{}, defaultLAPIURL: server.URL,
		settings:   Settings{MachineID: "machine", MachinePassword: "machine-secret", AlertLimit: 500},
		httpClient: server.Client(), machineClient: server.Client(),
	}
	page, err := m.ListAlerts(context.Background(), AlertQuery{Limit: 25, SinceHours: 24})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != 7 || page.Items[0].Country != "NL" {
		t.Fatalf("local alerts not listed: %+v %v", page, err)
	}
	// An explicit blocklist origin asks for those alerts; too large to list, it says so instead of
	// reporting CrowdSec as unreachable.
	if _, err := m.ListAlerts(context.Background(), AlertQuery{Limit: 25, SinceHours: 24, Origin: "CAPI"}); !errors.Is(err, ErrCrowdSecBlocklistTooLarge) || errors.Is(err, ErrCrowdSecUnreachable) {
		t.Fatalf("CAPI filter: %v, want ErrCrowdSecBlocklistTooLarge", err)
	}
	if _, err := m.ListAlerts(context.Background(), AlertQuery{Limit: 25, SinceHours: 24, Origin: "Lists"}); !errors.Is(err, ErrCrowdSecBlocklistTooLarge) {
		t.Fatalf("lists filter: %v", err)
	}
	if want := []string{"false", "true", "true"}; strings.Join(recorded, ",") != strings.Join(want, ",") {
		t.Fatalf("include_capi sent %v, want %v", recorded, want)
	}
	// Statistics read the same alerts: the blocklist pull must not empty them.
	recorded = nil
	stats, err := m.Stats(context.Background(), 24)
	if err != nil || stats.Alerts != 1 || slices.Contains(stats.Partial, "alerts") {
		t.Fatalf("stats: %+v %v", stats, err)
	}
	for _, v := range recorded {
		if v != "false" {
			t.Fatalf("stats asked for blocklist alerts: %v", recorded)
		}
	}
}

func TestMachineDecisionsLeaveOutBlocklistAlerts(t *testing.T) {
	var recorded []string
	server := capiLAPI(t, &recorded)
	defer server.Close()
	m := &crowdsecManager{
		store: noIntegration{}, secrets: noSecret{}, defaultLAPIURL: server.URL,
		settings:   Settings{MachineID: "machine", MachinePassword: "machine-secret", AlertLimit: 500},
		httpClient: server.Client(), machineClient: server.Client(),
	}
	decisions, err := m.listDecisionsMachine(context.Background())
	if err != nil || len(decisions) != 1 || decisions[0].ID != 7 {
		t.Fatalf("machine decisions: %+v %v", decisions, err)
	}
	if strings.Join(recorded, ",") != "false" {
		t.Fatalf("include_capi sent %v, want [false]", recorded)
	}
}

// A community blocklist holds ~15,000 decisions (~3 MB): more than the 1 MB ceiling of other LAPI
// answers and more than the alert limit, and still a complete, valid decision list.
func TestBouncerDecisionsReadTheWholeBlocklist(t *testing.T) {
	var body strings.Builder
	body.WriteString("[")
	const n = 15000
	for i := 0; i < n; i++ {
		if i > 0 {
			body.WriteString(",")
		}
		fmt.Fprintf(&body, `{"id":%d,"origin":"CAPI","scenario":"crowdsecurity/http-probing","scope":"Ip","type":"ban","value":"198.51.%d.%d","duration":"23h59m58.123456789s","until":"2026-11-28T17:00:00Z","uuid":"0b5a1c2e-7f00-4a00-9a00-%012d"}`, i+1, (i/250)%250, i%250, i)
	}
	body.WriteString("]")
	if body.Len() <= maxLAPIResponseBytes {
		t.Fatalf("fixture too small: %d bytes", body.Len())
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/decisions" || r.Header.Get("X-Api-Key") != "bouncer-key" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, body.String())
	}))
	defer server.Close()
	m := &crowdsecManager{
		store: noIntegration{}, secrets: noSecret{}, defaultLAPIURL: server.URL,
		settings:   Settings{APIKey: "bouncer-key", AlertLimit: 500, AlertLimitSet: true},
		httpClient: server.Client(), machineClient: server.Client(),
	}
	decisions, err := m.listDecisionsNative(context.Background())
	if err != nil || len(decisions) != n {
		t.Fatalf("decisions: %d %v, want %d", len(decisions), err, n)
	}
}

// The machine check reads one local alert: a blocklist pull would be refused by size and report valid
// credentials as failing.
func TestMachineCredentialCheckLeavesOutBlocklistAlerts(t *testing.T) {
	var recorded []string
	server := capiLAPI(t, &recorded)
	defer server.Close()
	m := &crowdsecManager{
		store: noIntegration{}, secrets: noSecret{}, defaultLAPIURL: server.URL,
		settings:   Settings{AlertLimit: 500},
		httpClient: server.Client(), machineClient: server.Client(),
	}
	if err := m.verifyMachineCredentials(context.Background(), "machine", "machine-secret"); err != nil {
		t.Fatalf("valid machine credentials rejected: %v", err)
	}
	if strings.Join(recorded, ",") != "false" {
		t.Fatalf("include_capi sent %v, want [false]", recorded)
	}
}

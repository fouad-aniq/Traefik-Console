package status

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	yamlv3 "gopkg.in/yaml.v3"

	"github.com/traefik-console/internal/config/yaml"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestConvergenceComparesSettingsAndAttachments(t *testing.T) {
	for _, tc := range []struct {
		name, desired, actual string
		loaded                bool
	}{
		{"stale middleware", "http:\n  middlewares:\n    security:\n      headers:\n        frameDeny: true\n", `{"middlewares":{"security@file":{"status":"enabled","headers":{"frameDeny":false}}}}`, false},
		{"changed service", "http:\n  services:\n    app:\n      loadBalancer:\n        servers:\n          - url: http://new.invalid\n", `{"services":{"app@file":{"status":"enabled","loadBalancer":{"servers":[{"url":"http://old.invalid"}]}}}}`, false},
		{"stale attachment", "http:\n  routers:\n    app:\n      service: app\n      rule: Host(`example.invalid`)\n      middlewares: [security]\n", `{"routers":{"app@file":{"status":"enabled","service":"app@file","rule":"Host(` + "`example.invalid`" + `)","middlewares":[]}}}`, false},
		{"matching middleware", "http:\n  middlewares:\n    security:\n      headers:\n        frameDeny: true\n", `{"middlewares":{"security@file":{"status":"enabled","headers":{"frameDeny":true}}}}`, true},
		{"plugin booleans and numbers exposed as strings", "http:\n  middlewares:\n    security:\n      plugin:\n        bouncer:\n          enabled: true\n          limit: 5\n          nested:\n            flags: [false, true]\n", `{"middlewares":{"security@file":{"status":"enabled","plugin":{"bouncer":{"enabled":"true","limit":"5","nested":{"flags":["false","true"]}}}}}}`, true},
		{"stale plugin boolean", "http:\n  middlewares:\n    security:\n      plugin:\n        bouncer:\n          enabled: true\n", `{"middlewares":{"security@file":{"status":"enabled","plugin":{"bouncer":{"enabled":"false"}}}}}`, false},
		{"stale plugin number", "http:\n  middlewares:\n    security:\n      plugin:\n        bouncer:\n          limit: 5\n", `{"middlewares":{"security@file":{"status":"enabled","plugin":{"bouncer":{"limit":"6"}}}}}`, false},
		{"empty plugin lists exposed as empty strings", "http:\n  middlewares:\n    security:\n      plugin:\n        bouncer:\n          clientTrustedIPs: []\n          forwardedHeadersTrustedIPs: []\n", `{"middlewares":{"security@file":{"status":"enabled","plugin":{"bouncer":{"clientTrustedIPs":"","forwardedHeadersTrustedIPs":""}}}}}`, true},
		{"populated plugin list never matches empty string", "http:\n  middlewares:\n    security:\n      plugin:\n        bouncer:\n          clientTrustedIPs: [192.0.2.0/24]\n", `{"middlewares":{"security@file":{"status":"enabled","plugin":{"bouncer":{"clientTrustedIPs":""}}}}}`, false},
		{"wrong plugin alias", "http:\n  middlewares:\n    security:\n      plugin:\n        bouncer:\n          enabled: true\n", `{"middlewares":{"security@file":{"status":"enabled","plugin":{"other":{"enabled":"true"}}}}}`, false},
		{"native booleans remain strict", "http:\n  middlewares:\n    security:\n      headers:\n        frameDeny: true\n", `{"middlewares":{"security@file":{"status":"enabled","headers":{"frameDeny":"true"}}}}`, false},
		{"plugin strings remain opaque", "http:\n  middlewares:\n    security:\n      plugin:\n        bouncer:\n          service: demo\n", `{"middlewares":{"security@file":{"status":"enabled","plugin":{"bouncer":{"service":"demo@file"}}}}}`, false},
		{"rejected plugin remains rejected", "http:\n  middlewares:\n    security:\n      plugin:\n        bouncer:\n          enabled: true\n", `{"middlewares":{"security@file":{"status":"disabled","plugin":{"bouncer":{"enabled":"true"}}}}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oc := NewObservationClient("http://registered.invalid")
			oc.httpClient.Transport = testTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.actual))}, nil
			})
			doc, _, e := yaml.ParseBoundedYAML(strings.NewReader(tc.desired), yaml.DefaultParserOptions())
			if e != nil {
				t.Fatal(e)
			}
			out, e := oc.VerifyConvergence(context.Background(), nil, doc)
			if e != nil || out.Loaded != tc.loaded {
				t.Fatal(out, e)
			}
		})
	}
}

func TestConvergenceRetriesTransientProviderRejection(t *testing.T) {
	doc, _, err := yaml.ParseBoundedYAML(strings.NewReader("http:\n  routers:\n    portainer:\n      service: app\n      middlewares: [crowdsec-bouncer]\n  middlewares:\n    crowdsec-bouncer:\n      plugin:\n        bouncer:\n          enabled: true\n"), yaml.DefaultParserOptions())
	if err != nil {
		t.Fatal(err)
	}
	responses := []string{
		`{"routers":{"portainer@file":{"status":"error","error":"invalid middleware crowdsec-bouncer@file"}},"middlewares":{}}`,
		`{"routers":{"portainer@file":{"status":"enabled","service":"app@file","middlewares":["crowdsec-bouncer@file"]}},"middlewares":{"crowdsec-bouncer@file":{"status":"enabled","plugin":{"bouncer":{"enabled":"true"}}}}}`,
	}
	calls := 0
	oc := NewObservationClient("http://registered.invalid")
	oc.httpClient.Transport = testTransport(func(*http.Request) (*http.Response, error) {
		body := responses[min(calls, len(responses)-1)]
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	out, err := oc.VerifyConvergenceWithRetry(context.Background(), nil, doc, time.Second)
	if err != nil || !out.Loaded || out.AttributableErr || calls < 2 {
		t.Fatalf("transient provider rejection was not retried: %+v calls=%d err=%v", out, calls, err)
	}
}

func TestConvergenceReturnsPersistentProviderRejection(t *testing.T) {
	doc, _, err := yaml.ParseBoundedYAML(strings.NewReader("http:\n  middlewares:\n    rejected:\n      headers:\n        frameDeny: true\n"), yaml.DefaultParserOptions())
	if err != nil {
		t.Fatal(err)
	}
	oc := NewObservationClient("http://registered.invalid")
	oc.httpClient.Transport = testTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"middlewares":{"rejected@file":{"status":"error","error":"invalid headers"}}}`))}, nil
	})
	out, err := oc.VerifyConvergenceWithRetry(context.Background(), nil, doc, 10*time.Millisecond)
	if err != nil || !out.AttributableErr || out.Loaded {
		t.Fatalf("persistent provider rejection was not retained: %+v err=%v", out, err)
	}
}

func parseNode(t *testing.T, src string) *yamlv3.Node {
	t.Helper()
	doc, _, err := yaml.ParseBoundedYAML(strings.NewReader(src), yaml.DefaultParserOptions())
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func observing(body string) *ObservationClient {
	oc := NewObservationClient("http://registered.invalid")
	oc.httpClient.Transport = testTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	return oc
}

// A router that was already disabled in Traefik (its plugin middleware cannot start) must not make an
// unrelated change fail: only what the operation created or changed is judged.
func TestConvergenceIgnoresUnhealthyResourcesThisChangeDidNotTouch(t *testing.T) {
	const router = "  routers:\n    portainer:\n      service: portainer\n      rule: Host(`p.example`)\n      middlewares: [crowdsec-bouncer]\n"
	before := parseNode(t, "http:\n"+router+"  services:\n    portainer:\n      loadBalancer:\n        servers:\n          - url: http://localhost:9443\n")
	after := parseNode(t, "http:\n"+router+"  services:\n    portainer:\n      loadBalancer:\n        servers:\n          - url: http://localhost:9443\n    plex:\n      loadBalancer:\n        servers:\n          - url: http://localhost:32400\n")
	const live = `{"routers":{"portainer@file":{"status":"disabled","error":["middleware crowdsec-bouncer@file does not exist"]}},` +
		`"services":{"portainer@file":{"status":"enabled","loadBalancer":{"servers":[{"url":"http://localhost:9443"}]}},"plex@file":{"status":"enabled","loadBalancer":{"servers":[{"url":"http://localhost:32400"}]}}}}`
	out, err := observing(live).VerifyConvergence(context.Background(), before, after)
	if err != nil || !out.Loaded || out.AttributableErr {
		t.Fatalf("an unrelated disabled router blocked the change: %+v err=%v", out, err)
	}
}

func TestConvergenceStillRejectsAChangedResourceThatIsUnhealthy(t *testing.T) {
	before := parseNode(t, "http:\n  services:\n    plex:\n      loadBalancer:\n        servers:\n          - url: http://old\n")
	after := parseNode(t, "http:\n  services:\n    plex:\n      loadBalancer:\n        servers:\n          - url: http://new\n")
	out, err := observing(`{"services":{"plex@file":{"status":"disabled","err":"boom"}}}`).VerifyConvergence(context.Background(), before, after)
	if err != nil || !out.AttributableErr || out.Loaded {
		t.Fatalf("a changed, rejected resource was accepted: %+v err=%v", out, err)
	}
	// A brand-new resource is judged too.
	out, err = observing(`{"services":{"plex@file":{"status":"error","err":"boom"}}}`).VerifyConvergence(context.Background(), nil, after)
	if err != nil || !out.AttributableErr {
		t.Fatalf("a created, rejected resource was accepted: %+v err=%v", out, err)
	}
}

func TestConvergenceStillRequiresTheChangedResourceToLoad(t *testing.T) {
	before := parseNode(t, "http:\n  services:\n    keep:\n      loadBalancer:\n        servers:\n          - url: http://k\n")
	after := parseNode(t, "http:\n  services:\n    keep:\n      loadBalancer:\n        servers:\n          - url: http://k\n    plex:\n      loadBalancer:\n        servers:\n          - url: http://p\n")
	// Traefik has not loaded the new service yet: not converged, even though the unchanged one is absent too.
	out, err := observing(`{"services":{}}`).VerifyConvergence(context.Background(), before, after)
	if err != nil || out.Loaded || out.AttributableErr {
		t.Fatalf("expected a pending (not loaded) outcome: %+v err=%v", out, err)
	}
}

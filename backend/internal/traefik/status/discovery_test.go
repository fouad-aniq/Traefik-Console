package status

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func observationFixture(t *testing.T, body string) *ObservationClient {
	t.Helper()
	c := NewObservationClient("http://registered.invalid")
	c.httpClient.Transport = testTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	t.Cleanup(c.Close)
	return c
}

func TestOverviewIncludesNestedProtocols(t *testing.T) {
	c := observationFixture(t, `{"http":{"routers":{"a@file":{"status":"enabled"}}},"tcp":{"routers":{"a@file":{"status":"enabled"}}},"udp":{"routers":{"a@file":{"status":"disabled"}}}}`)
	got := c.GetOverview(context.Background())
	if got.RoutesTotal != 3 || got.RoutesLoaded != 2 || got.TraefikStatus != "degraded" {
		t.Fatalf("incorrect multiprotocol overview: %+v", got)
	}
}

func TestDiagnosticsUsesSameCategoryPrecedence(t *testing.T) {
	for _, protocol := range []string{"http", "tcp", "udp"} {
		t.Run(protocol, func(t *testing.T) {
			flatServices, flatRouters := "services", "routers"
			if protocol != "http" {
				flatServices = protocol + "Services"
				flatRouters = protocol + "Routers"
			}
			// Flat tables win when both response forms are present.
			body := `{"` + flatServices + `":{"app@file":{"status":"enabled"}},"` + flatRouters + `":{"route@file":{"status":"enabled","service":"app@file"}},"` + protocol + `":{"services":{"app@file":{"status":"disabled"}},"routers":{}}}`
			got, err := observationFixture(t, body).DiagnoseNamedService(context.Background(), protocol, "app")
			if err != nil || got.RuntimeStatus != "enabled" || len(got.Routers) != 1 {
				t.Fatalf("inconsistent precedence: %+v, %v", got, err)
			}
			body = `{"` + protocol + `":{"services":{"app@file":{"status":"enabled"}},"routers":{"route@file":{"status":"enabled","service":"app@file"}}}}`
			got, err = observationFixture(t, body).DiagnoseNamedService(context.Background(), protocol, "app")
			if err != nil || len(got.Routers) != 1 {
				t.Fatalf("missing nested routers: %+v, %v", got, err)
			}
		})
	}
}

func TestObservationFailureIsNotAnEmptySuccess(t *testing.T) {
	c := NewObservationClient("http://registered.invalid")
	defer c.Close()
	c.httpClient.Transport = testTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })
	if _, err := c.FetchRawData(context.Background()); err == nil {
		t.Fatal("outage accepted")
	}
	got := c.GetOverview(context.Background())
	if got.Connected || !got.IsStale || got.TraefikStatus != "unavailable" {
		t.Fatalf("outage reported healthy: %+v", got)
	}
}

func TestStaticDiscoveryAndRuntimeProtocolsAreIndependent(t *testing.T) {
	static, err := DiscoverStatic([]byte("entryPoints:\n  secure: {}\n  web: {}\ncertificatesResolvers:\n  local:\n    acme:\n      storage: /etc/traefik/acme/local.json\nexperimental:\n  plugins:\n    example:\n      moduleName: example.invalid/plugin\n      version: v1.0.0\n"))
	if err != nil || len(static.Resolvers) != 1 || len(static.StaticPlugins) != 1 || strings.Join(static.EntryPoints, ",") != "secure,web" {
		t.Fatalf("static discovery: %+v %v", static, err)
	}
	if _, err := DiscoverStatic([]byte("entryPoints: [")); err == nil {
		t.Fatal("malformed static accepted")
	}
	for _, raw := range [][]byte{nil, []byte(" ")} {
		empty, err := DiscoverStatic(raw)
		if err != nil || len(empty.EntryPoints) != 0 || empty.Resolvers == nil {
			t.Fatal("empty config fabricated defaults")
		}
	}
	c := observationFixture(t, `{"services":{"same@docker":{"status":"enabled"},"fileonly@file":{"status":"enabled"}},"tcpServices":{"same@file":{"status":"enabled"}},"udp":{"services":{"datagram@file":{"status":"enabled"}}},"middlewares":{"headers@docker":{"status":"enabled"}}}`)
	runtime, err := c.ObserveRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	httpServices, httpMW := runtime.HTTP.ValidationMaps()
	tcpServices, _ := runtime.TCP.ValidationMaps()
	udpServices, _ := runtime.UDP.ValidationMaps()
	if !httpServices["same@docker"] || httpServices["same"] || httpServices["same@file"] || !httpServices["fileonly"] || httpServices["datagram"] || httpMW["headers"] || !httpMW["headers@docker"] || !tcpServices["same"] || !udpServices["datagram"] {
		t.Fatal("protocol/provider references conflated")
	}
	// No shared cache: a later observation sees the new runtime.
	c.httpClient.Transport = testTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })
	if snapshot, err := c.ObserveRuntime(context.Background()); snapshot != nil || !errors.Is(err, ErrObservationUnavailable) {
		t.Fatal("stale observation reused")
	}
}

func TestRuntimeRejectsNullAndMalformedResponses(t *testing.T) {
	for _, raw := range []string{"null", "[", "[]"} {
		if snapshot, err := observationFixture(t, raw).ObserveRuntime(context.Background()); snapshot != nil || !errors.Is(err, ErrObservationUnavailable) {
			t.Fatalf("invalid response accepted: %q", raw)
		}
	}
}

// countingTraefik answers /api/rawdata after a delay and counts the requests it served.
func countingTraefik(t *testing.T, delay time.Duration, fail *atomic.Bool) (*ObservationClient, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	c := NewObservationClient("http://registered.invalid")
	t.Cleanup(c.Close)
	c.httpClient.Transport = testTransport(func(r *http.Request) (*http.Response, error) {
		hits.Add(1)
		time.Sleep(delay)
		if fail != nil && fail.Load() {
			return nil, errors.New("offline")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"routers":{"web@file":{"rule":"Host(` + "`a`" + `)","status":"enabled"}}}`)), Header: http.Header{}, Request: r}, nil
	})
	return c, &hits
}

func TestConcurrentObservationsShareOneRequest(t *testing.T) {
	c, hits := countingTraefik(t, 50*time.Millisecond, nil)
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 10 { // a page loading middlewares, chains and routers at once, times three
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.ObserveRuntime(context.Background()); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal("a concurrent caller was rejected:", err)
	}
	if hits.Load() != 1 {
		t.Fatal("concurrent callers should share one request, got", hits.Load())
	}
}

func TestSharedObservationGivesEachCallerItsOwnCopy(t *testing.T) {
	c, hits := countingTraefik(t, 50*time.Millisecond, nil)
	results := make(chan map[string]any, 2)
	for range 2 {
		go func() { r, _ := c.FetchRawData(context.Background()); results <- r }()
	}
	a, b := <-results, <-results
	a["mutated"] = true
	if hits.Load() != 1 || b["mutated"] != nil {
		t.Fatal("callers must share the request but not the decoded data", hits.Load(), b)
	}
	// Completed: the next observation is a new request.
	_, _ = c.FetchRawData(context.Background())
	if hits.Load() != 2 {
		t.Fatal("a completed observation was reused", hits.Load())
	}
}

func TestObservationFailureIsNeverReused(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	c, hits := countingTraefik(t, 0, &fail)
	if _, err := c.FetchRawData(context.Background()); !errors.Is(err, ErrObservationUnavailable) {
		t.Fatal(err)
	}
	fail.Store(false)
	if _, err := c.FetchRawData(context.Background()); err != nil || hits.Load() != 2 {
		t.Fatal("a failure was cached", err, hits.Load())
	}
}

func TestObservationCallerCancellationDoesNotFailOthers(t *testing.T) {
	c, _ := countingTraefik(t, 80*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := c.FetchRawData(ctx); first <- err }()
	time.Sleep(10 * time.Millisecond)
	second := make(chan error, 1)
	go func() { _, err := c.FetchRawData(context.Background()); second <- err }()
	cancel()
	if err := <-first; err == nil {
		t.Fatal("cancelled caller kept waiting")
	}
	if err := <-second; err != nil {
		t.Fatal("another caller's cancellation failed the shared request", err)
	}
}

func TestVerificationNeverJoinsAnObservationStartedEarlier(t *testing.T) {
	c, hits := countingTraefik(t, 80*time.Millisecond, nil)
	go func() { _, _ = c.FetchRawData(context.Background()) }() // started before the write
	time.Sleep(10 * time.Millisecond)
	if _, err := c.fetchFreshRawData(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Fatal("verification joined a request started before the write", hits.Load())
	}
}

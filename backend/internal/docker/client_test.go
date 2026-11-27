package docker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func registration(host string) Registration {
	return Registration{DockerHost: host, TraefikContainer: "traefik", CrowdSecContainer: "crowdsec", AllowRestart: true}
}
func TestOperatorEndpointsAndTransportPolicy(t *testing.T) {
	for _, endpoint := range []string{"unix:///var/run/docker.sock", "tcp://socket-proxy:2375", "http://socket-proxy:2375", "https://socket-proxy:2376", "http://[::1]:2375"} {
		t.Run(endpoint, func(t *testing.T) {
			c, e := New(registration(endpoint))
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			transport := c.httpClient.Transport.(*http.Transport)
			if transport.Proxy != nil || c.httpClient.Timeout != 30*time.Second || c.httpClient.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
				t.Fatal("transport policy lost")
			}
			if strings.HasPrefix(endpoint, "tcp:") && !strings.HasPrefix(c.baseURL, "http:") {
				t.Fatal("TCP endpoint not mapped to HTTP")
			}
		})
	}
	for _, endpoint := range []string{"unix://host/path", "unix:///", "unix:///var/run/docker.sock?x=1", "unix:///a/../docker.sock", "http://user:password@proxy:2375", "http://proxy:2375/containers", "http://proxy:2375?x=1", "http://proxy:2375#fragment", "tcp://proxy", "tcp://proxy:0", "tcp://proxy:65536", "http://proxy:", "file:///var/run/docker.sock", "//proxy:2375"} {
		if c, e := New(registration(endpoint)); e == nil {
			c.Close()
			t.Fatal("unsafe endpoint accepted")
		}
	}
}
func TestRegistrationIsFixedAndDisabledModeFailsClosed(t *testing.T) {
	for _, mutate := range []func(*Registration){func(r *Registration) { r.TraefikContainer = "" }, func(r *Registration) { r.TraefikContainer = "../containers/other" }, func(r *Registration) { r.CrowdSecContainer = "bad name" }, func(r *Registration) { r.CrowdSecContainer = r.TraefikContainer }, func(r *Registration) { r.SecretsDir = "/" }} {
		r := registration("unix:///var/run/docker.sock")
		mutate(&r)
		if _, e := New(r); e == nil {
			t.Fatal("unsafe registration accepted")
		}
	}
	c, e := New(Registration{})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	ctx := context.Background()
	for _, operation := range []func() error{func() error { _, e := c.InspectTraefikContainer(ctx); return e }, func() error { _, e := c.RestartTraefikContainerAndConfirm(ctx); return e }, func() error { return c.ProbeTraefikSecretFile(ctx, "key") }, func() error { _, e := c.ListDecisions(ctx); return e }, func() error { return c.AddDecision(ctx, "192.0.2.1", "1h", "test") }, func() error { return c.DeleteDecision(ctx, "192.0.2.1") }, func() error { return c.RegisterBouncer(ctx, 1, strings.Repeat("a", 64)) }, func() error { return c.RevokeBouncer(ctx, 1) }} {
		if e := operation(); !errors.Is(e, ErrCapabilityDisabled) {
			t.Fatal("disabled operation did not fail closed", e)
		}
	}
}
func TestInputsDeniedBeforeAnyDockerRequest(t *testing.T) {
	c, e := New(registration("http://proxy:2375"))
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	c.httpClient.Transport = transportFunc(func(*http.Request) (*http.Response, error) { calls++; return response(500, ""), nil })
	ctx := context.Background()
	for _, operation := range []func() error{func() error { return c.ProbeTraefikSecretFile(ctx, "../private") }, func() error { return c.AddDecision(ctx, "--help", "1h", "test") }, func() error { return c.AddDecision(ctx, "192.0.2.1", "10000h", "test") }, func() error { return c.AddDecision(ctx, "192.0.2.1", "1h", "test\nnext") }, func() error { return c.DeleteDecision(ctx, "0.0.0.0/0; sh") }, func() error { return c.RegisterBouncer(ctx, -1, strings.Repeat("a", 64)) }, func() error { return c.RegisterBouncer(ctx, 1, "bad") }, func() error { return c.RevokeBouncer(ctx, 0) }} {
		if operation() == nil {
			t.Fatal("invalid operation accepted")
		}
	}
	if calls != 0 {
		t.Fatal("executor reached for invalid input")
	}
}
func TestRedirectDoesNotForwardToAnotherEndpoint(t *testing.T) {
	c, _ := New(registration("http://proxy:2375"))
	calls := 0
	c.httpClient.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		res := response(302, "")
		res.Header.Set("Location", "http://unregistered:2375/containers/anything/json")
		return res, nil
	})
	if _, e := c.InspectTraefikContainer(context.Background()); e == nil || calls != 1 {
		t.Fatal("redirect followed or accepted", e, calls)
	}
}
func TestOnlyTypedOperationsAreExported(t *testing.T) {
	expected := map[string]bool{"InspectTraefikContainer": true, "RestartTraefikContainerAndConfirm": true, "ProbeTraefikSecretFile": true, "CrowdSecEngine": true, "ListAlerts": true, "InspectAlert": true, "ListBouncers": true, "ListDecisions": true, "AddDecision": true, "DeleteDecision": true, "DeleteDecisionByID": true, "RegisterBouncer": true, "RevokeBouncer": true, "RegisterMachine": true, "RevokeMachine": true, "ListAllowlists": true, "CheckAllowlist": true, "CreateAllowlist": true, "AddAllowlistEntry": true, "RemoveAllowlistEntry": true, "DeleteAllowlist": true, "ListHubItems": true, "Close": true}
	typ := reflect.TypeOf((*Client)(nil))
	for i := 0; i < typ.NumMethod(); i++ {
		if !expected[typ.Method(i).Name] {
			t.Fatal("unexpected generic Docker surface", typ.Method(i).Name)
		}
	}
}
func TestExecIdentityCannotBecomeAnArbitraryDockerPath(t *testing.T) {
	c, _ := New(registration("http://proxy:2375"))
	calls := 0
	c.httpClient.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return response(201, `{"Id":"../../containers/other/restart"}`), nil
	})
	if _, e := c.execFixed(context.Background(), c.traefikRef, []string{"/usr/bin/test", "-r", "/registered"}); !errors.Is(e, ErrExecFailed) || calls != 1 {
		t.Fatal("untrusted exec identity used", e, calls)
	}
}
func TestOwnedRevocationHoldsOneOperationLockAcrossCommands(t *testing.T) {
	defer func(previous time.Duration) { operationQueueWait = previous }(operationQueueWait)
	operationQueueWait = 50 * time.Millisecond
	c, _ := New(registration("http://proxy:2375"))
	started := make(chan struct{})
	release := make(chan struct{})
	starts := 0
	c.httpClient.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/json") && strings.HasPrefix(r.URL.Path, "/containers/"):
			return response(200, `{"Id":"`+strings.Repeat("b", 64)+`","Name":"/crowdsec","State":{"Running":true}}`), nil
		case strings.HasSuffix(r.URL.Path, "/exec"):
			var body struct{ Cmd []string }
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid command payload")
			}
			if len(body.Cmd) < 2 || body.Cmd[0] != "cscli" {
				t.Error("unexpected executable")
			}
			return response(201, `{"Id":"`+strings.Repeat("c", 64)+`"}`), nil
		case strings.HasSuffix(r.URL.Path, "/start"):
			starts++
			if starts == 1 {
				close(started)
				<-release
				data := []byte(`[{"name":"traefik-console-op-7"}]`)
				frame := append([]byte{1, 0, 0, 0, 0, 0, 0, byte(len(data))}, data...)
				return response(200, string(frame)), nil
			}
			return response(200, ""), nil
		default:
			return response(200, `{"Running":false,"ExitCode":0}`), nil
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.RevokeBouncer(ctx, 7) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("revocation did not reach the executor")
	}
	_, e := c.InspectTraefikContainer(ctx)
	close(release)
	if !errors.Is(e, ErrOperationBusy) {
		t.Fatal("operation lock not held", e)
	}
	if e := <-done; e != nil || starts != 2 {
		t.Fatal("owned cleanup failed", e, starts)
	}
}

func TestIdentityAndRoleChecksBeforeRestart(t *testing.T) {
	for _, tc := range []struct{ name, id, container string }{{"wrong name", strings.Repeat("a", 64), "/other"}, {"not a full ID", "abc123", "/traefik"}, {"wrong role", strings.Repeat("a", 64), "/crowdsec"}} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := New(registration("http://proxy:2375"))
			calls := 0
			c.httpClient.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" {
					t.Fatal("mutation before ownership check")
				}
				raw, _ := json.Marshal(map[string]any{"Id": tc.id, "Name": tc.container, "State": map[string]bool{"Running": true}})
				return response(200, string(raw)), nil
			})
			if _, e := c.RestartTraefikContainerAndConfirm(context.Background()); !errors.Is(e, ErrContainerNotFound) || calls != 1 {
				t.Fatal("identity mismatch accepted", e, calls)
			}
		})
	}
	cReg := registration("http://proxy:2375")
	cReg.AllowRestart = false
	c, _ := New(cReg)
	c.httpClient.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("restart without opt-in reached Docker")
		return nil, nil
	})
	if _, e := c.RestartTraefikContainerAndConfirm(context.Background()); !errors.Is(e, ErrRestartDisabled) {
		t.Fatal("missing restart opt-in accepted", e)
	}
}

func TestExecRequiresExplicitFinishedSuccessfulResult(t *testing.T) {
	for _, result := range []string{`{}`, `{"Running":false}`, `{"ExitCode":0}`, `{"Running":true,"ExitCode":0}`, `{"Running":false,"ExitCode":1}`} {
		c, _ := New(registration("http://proxy:2375"))
		c.httpClient.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/exec"):
				return response(201, `{"Id":"`+strings.Repeat("c", 64)+`"}`), nil
			case strings.HasSuffix(r.URL.Path, "/start"):
				return response(200, ""), nil
			default:
				return response(200, result), nil
			}
		})
		if _, e := c.execFixed(context.Background(), c.traefikRef, []string{"/usr/bin/test", "-r", "/registered"}); !errors.Is(e, ErrExecFailed) {
			t.Fatal("incomplete/failed execution accepted", e)
		}
	}
}

func TestOperationsWaitTheirTurnInsteadOfFailing(t *testing.T) {
	c := &Client{httpClient: &http.Client{}, gate: make(chan struct{}, 1)}
	release, err := c.lockOperation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(30 * time.Millisecond); release() }()
	second, err := c.lockOperation(context.Background())
	if err != nil {
		t.Fatalf("a concurrent read was refused instead of queued: %v", err)
	}
	second()
}

func TestRevokeMachineDeletesTheMachineCscliLists(t *testing.T) {
	c, _ := New(registration("http://proxy:2375"))
	var commands [][]string
	var pending []string
	c.httpClient.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/json") && strings.HasPrefix(r.URL.Path, "/containers/"):
			return response(200, `{"Id":"`+strings.Repeat("b", 64)+`","Name":"/crowdsec","State":{"Running":true}}`), nil
		case strings.HasSuffix(r.URL.Path, "/exec"):
			var body struct{ Cmd []string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			commands = append(commands, body.Cmd)
			pending = body.Cmd
			return response(201, `{"Id":"`+strings.Repeat("c", 64)+`"}`), nil
		case strings.HasSuffix(r.URL.Path, "/start"):
			if len(pending) > 2 && pending[2] == "list" {
				// The shape cscli v1.8 prints for `cscli machines list -o json`.
				data := []byte(`[{"machineId":"localhost"},{"machineId":"traefik-console-machine-7","ipAddress":"10.0.3.3"}]`)
				frame := append([]byte{1, 0, 0, 0, 0, 0, 0, byte(len(data))}, data...)
				return response(200, string(frame)), nil
			}
			return response(200, ""), nil
		default:
			return response(200, `{"Running":false,"ExitCode":0}`), nil
		}
	})
	if e := c.RevokeMachine(context.Background(), 7); e != nil {
		t.Fatal(e)
	}
	last := commands[len(commands)-1]
	if strings.Join(last, " ") != "cscli machines delete traefik-console-machine-7" {
		t.Fatal("the registered machine was not deleted", commands)
	}
}

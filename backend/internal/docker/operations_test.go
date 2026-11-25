package docker

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestIdentityMismatchCannotRestart(t *testing.T) {
	calls := 0
	m := &Client{traefikRef: "traefik", allowTraefikRestart: true, baseURL: "http://docker", gate: make(chan struct{}, 1)}
	m.httpClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" {
			t.Fatal("side effect before identity verification")
		}
		return response(200, `{"Id":"`+strings.Repeat("a", 64)+`","Name":"/other","State":{"Running":true}}`), nil
	})}
	if _, e := m.RestartTraefikContainerAndConfirm(context.Background()); e == nil || calls != 1 {
		t.Fatal("wrong owner accepted", e, calls)
	}
}
func TestExecRequiresSuccessfulExitAndDoesNotExposeOutput(t *testing.T) {
	m := &Client{baseURL: "http://docker"}
	m.httpClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/exec"):
			return response(201, `{"Id":"`+strings.Repeat("b", 64)+`"}`), nil
		case strings.HasSuffix(r.URL.Path, "/start"):
			return response(200, "synthetic-private-output"), nil
		default:
			return response(200, `{"Running":false,"ExitCode":7}`), nil
		}
	})}
	out, e := m.execFixed(context.Background(), strings.Repeat("a", 64), []string{"cscli", "version"})
	if e != ErrExecFailed || len(out) != 0 {
		t.Fatal("nonzero exit treated as success", e)
	}
}
func TestPlainEnvironmentOverridesFileAreNotReportedAsMounted(t *testing.T) {
	m := &Client{secretRoot: "/registered/secrets"}
	got := m.secretFileEnv(map[string]string{"TOKEN": "synthetic", "TOKEN_FILE": "/registered/secrets/token.secret", "OK_FILE": "/registered/secrets/ok.secret", "ESCAPE_FILE": "/other/private"})
	if len(got) != 1 || got["OK_FILE"] == "" {
		t.Fatal("unsafe or shadowed file environment exposed")
	}
}

func TestDecisionsAcceptNestedAndFlatWithoutDroppingFlatRows(t *testing.T) {
	for _, raw := range []string{`[{"decisions":[{"id":1,"value":"192.0.2.1","scope":"Ip"}]}]`, `[{"id":1,"value":"192.0.2.1","scope":"Ip"}]`} {
		got, e := parseDecisions([]byte(raw))
		if e != nil || len(got) != 1 || got[0].Value != "192.0.2.1" {
			t.Fatal("decision lost", e, got)
		}
	}
	for _, raw := range []string{`[{"unexpected":true}]`, `[null]`, `{"error":"private"}`} {
		if _, e := parseDecisions([]byte(raw)); e == nil {
			t.Fatal("malformed decision accepted")
		}
	}
}

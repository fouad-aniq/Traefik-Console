package crowdsec

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestLAPIPolicyRejectsArbitraryOriginAndRedirect(t *testing.T) {
	calls := 0
	m := &crowdsecManager{defaultLAPIURL: "http://registered.invalid", httpClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "registered.invalid" {
			t.Error("credential escaped registered origin")
		}
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"http://unapproved.invalid/capture"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}}
	if e := m.verifyLAPIConnection(context.Background(), "http://arbitrary.invalid", "synthetic"); e == nil || calls != 0 {
		t.Fatal("arbitrary origin fetched")
	}
	if e := m.verifyLAPIConnection(context.Background(), m.defaultLAPIURL, "synthetic"); e == nil || calls != 1 {
		t.Fatal("redirect followed", calls, e)
	}
	for _, u := range []string{"http://user:pass@registered.invalid", "http://registered.invalid?token=a", "file:///etc/passwd", "http://registered.invalid/path"} {
		if _, e := registeredURL(u); e == nil {
			t.Fatal("unsafe registration accepted")
		}
	}
}

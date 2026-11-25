package updates

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSemverPrecedence(t *testing.T) {
	ordered := []string{"0.1.0-alpha", "0.1.0-alpha.1", "0.1.0-rc.1", "0.1.0-rc.2", "0.1.0-rc.10", "0.1.0", "0.1.1", "0.2.0", "1.0.0"}
	for i := 0; i+1 < len(ordered); i++ {
		a, okA := parseSemver(ordered[i])
		b, okB := parseSemver("v" + ordered[i+1])
		if !okA || !okB || a.compare(b) != -1 || b.compare(a) != 1 {
			t.Fatalf("%s must be below %s", ordered[i], ordered[i+1])
		}
	}
	for _, bad := range []string{"dev", "0.1", "1.2.3-", "1.2.3-rc..1", "1.2.x", "1.2.3-rc/1", "1.2.3-<x>"} {
		if _, ok := parseSemver(bad); ok {
			t.Fatalf("%q must not parse", bad)
		}
	}
}

func TestNoCheckForLocalBuildsOrWhenDisabled(t *testing.T) {
	if New("dev", true) != nil || New("0.1.0", false) != nil {
		t.Fatal("dev builds and a disabled check must not create a checker")
	}
	var c *Checker
	if c.Latest() != nil {
		t.Fatal("a nil checker reports no update")
	}
}

const releases = `[
 {"tag_name":"v0.2.0-rc.1","prerelease":true},
 {"tag_name":"v0.1.1","prerelease":false},
 {"tag_name":"v0.3.0","draft":true},
 {"tag_name":"v0.1.0-rc.3","prerelease":true},
 {"tag_name":"not-a-version"}
]`

func server(t *testing.T, body string, hits *int32) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func TestPicksTheNewestReleaseOfTheSameChannel(t *testing.T) {
	var hits int32
	s := server(t, releases, &hits)
	for _, tc := range []struct{ current, want string }{
		{"0.1.0-rc.3", "0.2.0-rc.1"}, // a pre-release build follows pre-releases too
		{"0.1.0", "0.1.1"},           // a stable build only stable releases; drafts are ignored
		{"0.1.1", ""},
	} {
		c := New(tc.current, true)
		c.URL = s.URL
		c.Refresh(context.Background())
		got := c.Latest()
		if (got == nil) != (tc.want == "") || got != nil && (got.Version != tc.want || got.URL != "https://github.com/"+Repository+"/releases/tag/v"+tc.want) {
			t.Fatalf("current %s: got %+v, want %q", tc.current, got, tc.want)
		}
	}
}

func TestRefreshesInTheBackgroundAndKeepsTheAnswerOnErrors(t *testing.T) {
	var hits int32
	s := server(t, releases, &hits)
	c := New("0.1.0", true)
	c.URL = s.URL
	clock := time.Unix(1000, 0)
	c.now = func() time.Time { return clock }

	if c.Latest() != nil { // first call: nothing known yet, a refresh starts
		t.Fatal("no answer before the first check")
	}
	waitFor(t, func() bool { return c.Latest() != nil })
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("fresh cache must not call GitHub again, hits=%d", hits)
	}
	clock = clock.Add(c.MaxAge) // stale: the next read refreshes with the ETag (304 keeps the answer)
	c.Latest()
	waitFor(t, func() bool { return atomic.LoadInt32(&hits) == 2 })
	waitFor(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return !c.running })
	if got := c.Latest(); got == nil || got.Version != "0.1.1" {
		t.Fatalf("not modified must keep the known release, got %+v", got)
	}
	s.Close() // network errors keep the last answer too
	clock = clock.Add(c.MaxAge)
	c.Refresh(context.Background())
	if got := c.Latest(); got == nil || got.Version != "0.1.1" {
		t.Fatalf("an error must keep the known release, got %+v", got)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

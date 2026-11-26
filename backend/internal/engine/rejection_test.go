package engine

import (
	"errors"
	"strings"
	"testing"
)

func TestRuntimeRejectionCarriesTraefiksMessage(t *testing.T) {
	r := newRuntimeRejection("http.routers 'authentik-secure@file' rejected by Traefik:\n plugin: unknown plugin type: bouncer")
	if !errors.Is(r, errRuntimeRejected) {
		t.Fatal("must still be a runtime rejection")
	}
	if code, _ := safeFailureReason(r); code != "RUNTIME_REJECTED" {
		t.Fatal(code)
	}
	if strings.Contains(r.detail, "\n") || !strings.Contains(r.detail, "unknown plugin type: bouncer") {
		t.Fatalf("detail: %q", r.detail)
	}
	if long := newRuntimeRejection(strings.Repeat("x", 1000)); len(long.detail) > 310 {
		t.Fatal("detail must be bounded")
	}
}

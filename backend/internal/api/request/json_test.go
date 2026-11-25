package request

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/traefik-console/internal/models"
)

type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (brokenBody) Close() error             { return nil }

func TestRequestLimitsAndNullScope(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		policy     NullPolicy
		want       int
	}{
		{"valid", `{"type":"headers","spec":{}}`, RejectNulls, 200},
		{"opaque nested null", `{"type":"plugin","spec":{"nested":[null,{"optional":null}]}}`, SpecNulls, 200},
		{"opaque null rejected by default", `{"spec":{"nested":null}}`, RejectNulls, 400},
		{"envelope null", `{"type":"headers","spec":{},"secret_refs":null}`, SpecNulls, 400},
		{"spec must be an object", `{"type":"headers","spec":null}`, SpecNulls, 400},
		{"unknown field", `{"unexpected":true}`, RejectNulls, 400},
		{"duplicate", `{"type":"headers","type":"plugin"}`, RejectNulls, 400},
		{"nested duplicate", `{"spec":{"a":1,"a":2}}`, SpecNulls, 400},
		{"trailing object", `{} {}`, RejectNulls, 400},
		{"trailing garbage", `{} x`, RejectNulls, 400},
		{"invalid UTF8", string([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}), RejectNulls, 400},
		{"array root", `[]`, RejectNulls, 400},
		{"empty", "", RejectNulls, 400},
		{"depth", `{"spec":` + strings.Repeat("[", 40) + "1" + strings.Repeat("]", 40) + "}", SpecNulls, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			var out models.MiddlewareConfig
			ok := DecodeObject(w, httptest.NewRequest("PUT", "http://localhost/api", strings.NewReader(tc.body)), &out, Options{MaxBytes: 1024, NullPolicy: tc.policy})
			if ok != (tc.want == 200) || w.Code != tc.want {
				t.Fatalf("decode: %v %d %s", ok, w.Code, w.Body.String())
			}
		})
	}
}

func TestRequestReadFailuresAreNotOversize(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		r := httptest.NewRequest("POST", "http://localhost/api", nil)
		r.Body = brokenBody{}
		if cancelled {
			ctx, cancel := context.WithCancel(r.Context())
			cancel()
			r = r.WithContext(ctx)
		}
		w := httptest.NewRecorder()
		var out map[string]any
		if DecodeObject(w, r, &out, Options{}) || w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct {
		body string
		want int
	}{{`{}`, 200}, {`{} `, 413}} {
		w := httptest.NewRecorder()
		var out map[string]any
		ok := DecodeObject(w, httptest.NewRequest("POST", "http://localhost/api", strings.NewReader(tc.body)), &out, Options{MaxBytes: 2})
		if ok != (tc.want == 200) || w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestRequestCompatibilityPoliciesRemainExplicit(t *testing.T) {
	r := httptest.NewRequest("PUT", "http://localhost/api", strings.NewReader(`{"type":"headers","Type":"plugin","Spec":{"Case":null,"case":1}}`))
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	var out models.MiddlewareConfig
	if !DecodeObject(w, r, &out, Options{NullPolicy: SpecNulls}) || out.Type != "plugin" || len(out.Spec) != 2 {
		t.Fatal("unapproved case/media policy change", w.Body.String())
	}
}

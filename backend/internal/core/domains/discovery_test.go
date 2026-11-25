package domains

import (
	"context"
	"errors"
	"testing"
)

type discoveryFiles struct {
	raw []byte
	err error
}

func (f discoveryFiles) ReadStaticConfig() ([]byte, error) { return f.raw, f.err }

func TestExternalResolverBindingNeedsOnlyStaticDiscovery(t *testing.T) {
	m := &domainManager{store: newStubStore(), files: discoveryFiles{raw: []byte("certificatesResolvers:\n  external:\n    acme:\n      storage: /etc/traefik/acme/external.json\n")}}
	if err := m.validateResolverBinding(context.Background(), "external", "example.invalid", nil); err != nil {
		t.Fatalf("static resolver required runtime API: %v", err)
	}
	if err := m.validateResolverBinding(context.Background(), "absent", "example.invalid", nil); !errors.Is(err, ErrDomainResolverNotFound) {
		t.Fatalf("absent resolver accepted: %v", err)
	}
	m.files = discoveryFiles{err: errors.New("static unavailable")}
	if err := m.validateResolverBinding(context.Background(), "external", "example.invalid", nil); err == nil || errors.Is(err, ErrDomainResolverNotFound) {
		t.Fatalf("read failure treated as absence: %v", err)
	}
	m.files = discoveryFiles{raw: []byte("certificatesResolvers: [")}
	if err := m.validateResolverBinding(context.Background(), "external", "example.invalid", nil); err == nil || errors.Is(err, ErrDomainResolverNotFound) {
		t.Fatalf("invalid YAML treated as absence: %v", err)
	}
}

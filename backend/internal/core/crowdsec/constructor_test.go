package crowdsec

import (
	"crypto/tls"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCrowdSecSettingsRequireCompleteRoleCredentials(t *testing.T) {
	for _, settings := range []Settings{{BouncerCertFile: "bouncer.pem"}, {MachineKeyFile: "machine.key"}} {
		_, err := NewCrowdSecManagerWithOptions(noIntegration{}, noSecret{}, nil, "http://crowdsec:8080", settings, nil, nil)
		if err == nil {
			t.Fatalf("incomplete role credentials accepted: %+v", settings)
		}
	}
}

func TestCrowdSecPrivateCALoading(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := integrationTLSConfig(caFile, "", "")
	if err != nil || config == nil || config.RootCAs == nil {
		t.Fatalf("private CA was not loaded safely: %+v %v", config, err)
	}
	if config.MinVersion != tls.VersionTLS12 {
		t.Fatal("TLS 1.2 minimum was not retained")
	}
	if _, err := integrationTLSConfig(caFile+"-missing", "", ""); err == nil {
		t.Fatal("missing CA accepted")
	}
}

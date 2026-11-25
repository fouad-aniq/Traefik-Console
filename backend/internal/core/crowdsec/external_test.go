package crowdsec

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/traefik-console/internal/config/secrets"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
)

type externalStore struct {
	db.ItemReader
	db.SecretReader
}

func (externalStore) GetItem(context.Context, models.ItemKind, string) (*models.ConfigItemRow, error) {
	return nil, nil
}

type externalSecrets struct {
	values map[string]string
}

func (s *externalSecrets) PrepareSecret(_ context.Context, name, purpose, _, value string) (*models.SecretRow, error) {
	return &models.SecretRow{Name: name, Purpose: purpose, FilePath: "/manager-only/" + name, EncryptedValue: []byte(value)}, nil
}

func (s *externalSecrets) DecryptSecret(_ context.Context, name string) (string, error) {
	if value := s.values[name]; value != "" {
		return value, nil
	}
	return "", secrets.ErrSecretNotFound
}

type externalApplier struct{ mutation models.Mutation }

func (a *externalApplier) Apply(_ context.Context, mutation models.Mutation) (*models.OperationResult, error) {
	a.mutation = mutation
	return &models.OperationResult{NoChange: true}, nil
}

func (a *externalApplier) Preview(_ context.Context, mutation models.Mutation) (*models.OperationPreview, error) {
	a.mutation = mutation
	return &models.OperationPreview{}, nil
}

func TestExternalConfigurationOnlyChangesOwnConnection(t *testing.T) {
	var requestKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestKey = r.Header.Get("X-Api-Key")
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()
	applier := &externalApplier{}
	manager := &crowdsecManager{
		store: externalStore{}, secrets: &externalSecrets{}, applier: applier,
		settings: Settings{ReadTimeout: 20 * time.Second, AlertLimit: 500},
	}
	_, err := manager.ConfigureBouncer(context.Background(), CrowdSecConfig{
		IntegrationMode: "external", LAPIURL: server.URL, LAPIKey: "browser-secret",
		ReadTimeoutSeconds: 30, AlertLimit: 250,
	}, models.WriteOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if requestKey != "browser-secret" {
		t.Fatal("external connection did not verify the supplied bouncer key")
	}
	if len(applier.mutation.Upserts) != 1 || applier.mutation.Upserts[0].Kind != models.KindCrowdSec || len(applier.mutation.Deletes) != 0 {
		t.Fatalf("external mode touched Traefik resources: %+v", applier.mutation)
	}
	if len(applier.mutation.Secrets) != 1 || applier.mutation.Secrets[0].Purpose != "crowdsec_manager_lapi" {
		t.Fatalf("external secret crossed the manager boundary: %+v", applier.mutation.Secrets)
	}
	if strings.Contains(string(applier.mutation.Upserts[0].Payload), "browser-secret") {
		t.Fatal("secret was serialized into the configuration row")
	}
	var saved CrowdSecConfig
	if json.Unmarshal(applier.mutation.Upserts[0].Payload, &saved) != nil || saved.Enabled || saved.IntegrationMode != "external" || saved.ReadTimeoutSeconds != 30 || saved.AlertLimit != 250 {
		t.Fatalf("unexpected external configuration: %+v", saved)
	}
}

func TestExternalConfigurationEnvironmentKeyIsAuthoritative(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "environment-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()
	applier := &externalApplier{}
	manager := &crowdsecManager{
		store: externalStore{}, secrets: &externalSecrets{}, applier: applier,
		defaultLAPIURL: server.URL,
		settings:       Settings{APIKey: "environment-secret", ReadTimeout: 20 * time.Second, AlertLimit: 500},
	}
	_, err := manager.ConfigureBouncer(context.Background(), CrowdSecConfig{
		IntegrationMode: "external", LAPIURL: server.URL, LAPIKey: "browser-secret",
	}, models.WriteOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(applier.mutation.Secrets) != 0 {
		t.Fatal("environment secret was replaced by a UI value")
	}
}

func TestSharedMTLSAuthenticatesWithoutLoginOrCredentialHeaders(t *testing.T) {
	certificate, key := syntheticClientCertificate(t)
	var mu sync.Mutex
	var loginCalls int
	var headers []http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		headers = append(headers, r.Header.Clone())
		switch r.URL.Path {
		case "/v1/watchers/login":
			loginCalls++
		case "/v1/alerts", "/v1/decisions":
			fmt.Fprint(w, `[]`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	secretStore := &externalSecrets{values: map[string]string{
		crowdsecClientCertName: certificate, crowdsecClientKeySecret: key,
	}}
	manager := &crowdsecManager{
		store: externalStore{}, secrets: secretStore, defaultLAPIURL: server.URL,
		settings:   Settings{ReadTimeout: 20 * time.Second, AlertLimit: 500},
		httpClient: server.Client(), machineClient: server.Client(),
	}
	tlsConfig, present, err := manager.tlsForRole(context.Background(), false)
	if err != nil || !present || len(tlsConfig.Certificates) != 1 {
		t.Fatalf("shared client certificate was not loaded: %t %v", present, err)
	}
	if _, err := manager.listDecisionsNative(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.verifyMachineCredentials(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	if loginCalls != 0 {
		t.Fatal("mTLS attempted machine login")
	}
	for _, header := range headers {
		if header.Get("X-Api-Key") != "" || header.Get("Authorization") != "" {
			t.Fatal("mTLS leaked an API key or bearer header")
		}
	}
}

func syntheticClientCertificate(t *testing.T) (string, string) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkixName("console-test"),
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}

func pkixName(commonName string) pkix.Name { return pkix.Name{CommonName: commonName} }

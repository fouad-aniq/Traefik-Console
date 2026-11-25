package status

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestACMEInventoryMapsPerResolverAndReportsPartialData(t *testing.T) {
	root := t.TempDir()
	cfg := []byte("certificatesResolvers:\n  first:\n    acme:\n      storage: /etc/traefik/acme/shared.json\n  second:\n    acme:\n      storage: /etc/traefik/acme/shared.json\n  external:\n    acme:\n      storage: /unmapped/other.json\n")
	observer := NewACMEObserver(root, "/etc/traefik/acme")
	empty := observer.Observe(cfg)
	if !empty.Partial || len(empty.Observations) != 3 {
		t.Fatal(empty)
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"*.example.invalid", "example.invalid"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	cert := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	raw, _ := json.Marshal(map[string]any{"first": map[string]any{"Certificates": []any{map[string]any{"domain": map[string]any{"main": "example.invalid"}, "certificate": cert}, map[string]any{"certificate": "invalid"}}}, "second": map[string]any{"Certificates": []any{}}})
	os.WriteFile(filepath.Join(root, "shared.json"), raw, 0600)
	inventory := observer.Observe(cfg)
	if !inventory.Partial || len(inventory.Certificates) != 1 || inventory.Find("first", []string{"app.example.invalid"}) == nil || inventory.Find("second", []string{"app.example.invalid"}) != nil {
		t.Fatal(inventory)
	}
	states := map[string]string{}
	for _, o := range inventory.Observations {
		states[o.Resolver] = o.State
	}
	if states["first"] != "incomplete" || states["second"] != "empty" || states["external"] != "not_mapped" {
		t.Fatal(states)
	}
	response, _ := json.Marshal(inventory)
	if strings.Contains(string(response), root) || strings.Contains(string(response), "shared.json") || strings.Contains(string(response), cert) {
		t.Fatal("ACME response disclosed private storage details")
	}
	os.Remove(filepath.Join(root, "shared.json"))
	os.Symlink(filepath.Join(t.TempDir(), "outside"), filepath.Join(root, "shared.json"))
	inventory = observer.Observe(cfg)
	if !inventory.Partial || len(inventory.Certificates) != 0 {
		t.Fatal("symlink observation succeeded")
	}
}

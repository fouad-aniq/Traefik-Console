package certificates

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/traefik-console/internal/config/secrets"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/engine/enginetest"
	"github.com/traefik-console/internal/models"
)

func TestCertificateMismatchCannotWriteMaterialAndCoverageIsPreserved(t *testing.T) {
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	sql, e := db.InitDB(filepath.Join(root, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer sql.Close()
	key := make([]byte, 32)
	s := db.NewStore(sql, key)

	sm, _ := secrets.New(filepath.Join(root, "secrets"), filepath.Join(root, "secrets"), key, s)
	mgr := NewCertificateManager(s, sm, &enginetest.DirectApplier{Store: s})
	k1, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	k2, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"example.invalid"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, cert, cert, &k1.PublicKey, k1)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	bad, _ := x509.MarshalPKCS8PrivateKey(k2)
	badPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: bad})
	ctx := context.Background()
	if _, e = mgr.ImportCertificate(ctx, "audit", "server", string(certPEM), string(badPEM), models.WriteOptions{}); e == nil {
		t.Fatal("mismatched certificate accepted")
	}
	rows, _ := s.ListSecrets(ctx)
	if len(rows) != 0 {
		t.Fatal("invalid pair wrote secrets")
	}
	good, _ := x509.MarshalPKCS8PrivateKey(k1)
	goodPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: good})
	out, e := mgr.ImportCertificate(ctx, "audit", "server", string(certPEM), string(goodPEM), models.WriteOptions{})
	if e != nil || out.Resource.Config.Fingerprint == "" {
		t.Fatal("valid import failed", e)
	}
	if _, e = s.CommitOperation(ctx, db.Commit{Mutation: models.Mutation{Upserts: []models.ConfigItemRow{{Kind: models.KindDomain, Name: "site", Scope: models.ScopeDynamic, Payload: []byte(`{"main":"different.invalid","method":"files","certificate":"audit"}`)}}}}); e != nil {
		t.Fatal(e)
	}
	if _, e = mgr.ImportCertificate(ctx, "audit", "server", string(certPEM), string(goodPEM), models.WriteOptions{}); e == nil {
		t.Fatal("replacement lost domain coverage")
	}
}

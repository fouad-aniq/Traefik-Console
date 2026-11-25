package certificates

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
)

type certificateRows struct {
	db.ItemReader
	rows []models.ConfigItemRow
}

func (s certificateRows) ListItemsByKind(context.Context, models.ItemKind) ([]models.ConfigItemRow, error) {
	return s.rows, nil
}

func TestCertificateListingDerivesStatusFromExpiry(t *testing.T) {
	// Synthetic metadata tests payload/expiry projection; cryptographic imports have separate tests.
	payload, _ := json.Marshal(models.CertificateConfig{NotAfter: time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339), Status: "stored", Issuer: "live"})
	m := NewCertificateManager(certificateRows{rows: []models.ConfigItemRow{{Kind: models.KindTLS, Name: "cert", Version: 2, Payload: payload}}}, nil, nil)
	rows, err := m.ListCertificates(context.Background())
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	if got := rows[0]; got.Config.Status != "expiring" || got.Config.Issuer != "live" || got.Version != 2 {
		t.Fatalf("unexpected projection: %+v", got)
	}
	bad := NewCertificateManager(certificateRows{rows: []models.ConfigItemRow{{Payload: json.RawMessage(`{`)}}}, nil, nil)
	if _, err := bad.ListCertificates(context.Background()); err == nil {
		t.Fatal("corrupt payload accepted")
	}
}

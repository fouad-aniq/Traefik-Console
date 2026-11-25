package domains

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/traefik-console/internal/models"
)

func TestDomainListingKeepsComputedUsageAndVersion(t *testing.T) {
	row := &models.ConfigItemRow{Kind: models.KindDomain, Name: "site", Version: 4, Payload: json.RawMessage(`{"main":"live.invalid","resolver":"live-resolver","used_by":["stored"]}`)}
	router := activeRow(t, models.KindRouter, "consumer", models.RouterConfig{DomainName: "site"})
	rows, err := newTestManager(row, router).ListDomains(context.Background())
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	got := rows[0]
	if len(got.Config.UsedBy) != 1 || got.Config.UsedBy[0] != "consumer" || got.Config.Main != "live.invalid" || got.Version != 4 {
		t.Fatalf("unexpected projection: %+v", got)
	}
	corrupt := &models.ConfigItemRow{Kind: models.KindDomain, Name: "bad", Payload: json.RawMessage(`{`)}
	if _, err := newTestManager(corrupt).ListDomains(context.Background()); err == nil {
		t.Fatal("corrupt payload accepted")
	}
}

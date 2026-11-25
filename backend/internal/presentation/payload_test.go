package presentation

import (
	"testing"

	"github.com/traefik-console/internal/models"
)

func TestDecodeUIConfigReadsTheCommittedPayload(t *testing.T) {
	type config struct{ Value, Removed string }
	cfg, err := DecodeUIConfig[config](models.ConfigItemRow{Payload: []byte(`{"Value":"v","Removed":"r"}`)})
	if err != nil || cfg.Value != "v" || cfg.Removed != "r" {
		t.Fatal(cfg, err)
	}
	if _, err = DecodeUIConfig[config](models.ConfigItemRow{Payload: []byte(`{`)}); err == nil {
		t.Fatal("corrupt payload accepted")
	}
}

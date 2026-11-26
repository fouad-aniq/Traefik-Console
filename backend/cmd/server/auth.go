package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/traefik-console/internal/api/authgate"
	"github.com/traefik-console/internal/auth"
	"github.com/traefik-console/internal/config"
	"github.com/traefik-console/internal/config/filesystem"
)

// openAuth opens the private auth database. secretKey may be nil for CLI
// commands, which never read the external provider's client secret.
func openAuth(opts *config.Options, secretKey []byte) (*auth.Store, *auth.Service, error) {
	if err := filesystem.EnsureDir(filepath.Dir(opts.Auth.DBPath)); err != nil {
		return nil, nil, fmt.Errorf("auth database directory: %w", err)
	}
	store, err := auth.OpenStore(opts.Auth.DBPath)
	if err != nil {
		return nil, nil, err
	}
	svc := auth.NewService(store, auth.Config{
		IdleTimeout: opts.Auth.IdleTimeout, MaxLifetime: opts.Auth.MaxLifetime, SecretKey: secretKey,
	})
	return store, svc, nil
}

func announceSetup(svc *auth.Service) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	token, err := svc.PrepareSetup(ctx)
	if err != nil {
		return fmt.Errorf("prepare setup: %w", err)
	}
	if token == "" {
		return nil
	}
	// Shown only while no administrator exists: the token is replaced at every start until then,
	// deleted once the administrator is created, and refused for any other use.
	diagLog.Infof("No administrator yet: open Traefik Console and enter this one-time setup token: %s", token)
	return nil
}

func newAuthGate(opts *config.Options, svc *auth.Service) *authgate.Gate {
	return authgate.New(svc, authgate.Options{CookieSecure: opts.Auth.CookieSecure, TrustedProxies: opts.Auth.TrustedProxies})
}

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	api "github.com/traefik-console/internal/api/handlers"
	"github.com/traefik-console/internal/api/middleware"
	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/config/secrets"
	"github.com/traefik-console/internal/engine"
	catalog "github.com/traefik-console/internal/traefik/plugins"
	"github.com/traefik-console/internal/traefik/status"
	"github.com/traefik-console/internal/traefik/validation"
)

// 4. Main HTTP Server (`./server` or `./server server`)
func runServer() error {
	opts, runtimeClient, err := loadOptions()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	defer runtimeClient.Close()

	state, err := openState(opts)
	if err != nil {
		return fmt.Errorf("open state: %w", err)
	}
	defer state.Close()

	store := state.store
	authStore, authSvc, err := openAuth(opts, state.key)
	if err != nil {
		return fmt.Errorf("authentication: %w", err)
	}
	defer authStore.Close()
	if err = announceSetup(authSvc); err != nil {
		return fmt.Errorf("authentication: %w", err)
	}
	filesMgr, err := files.New(opts.TraefikStaticPath, opts.TraefikDynamicDir)
	if err != nil {
		return fmt.Errorf("configuration files: %w", err)
	}

	secretsMgr, err := secrets.New(opts.SecretsDir, opts.RuntimeSecretsDir, state.key, store)
	if err != nil {
		return fmt.Errorf("runtime secrets: %w", err)
	}

	inspector := status.NewObservationClient(opts.TraefikAPIURL)
	defer inspector.Close()

	validator := validation.NewConfigValidator()
	deployCoord := newOperations(opts, store, filesMgr, secretsMgr, inspector, validator, runtimeClient)
	timeoutBudget := engine.NewTimeoutBudget(opts.VerifyConvergenceTo)
	recoveryCtx, cancelRecovery := context.WithTimeout(context.Background(), timeoutBudget.Recovery)
	// An operation that cannot be reverted must not keep the console down: the API refuses writes
	// (423) until the operator recovers or abandons it from the History page.
	if err = prepareServer(recoveryCtx, store, deployCoord); err != nil {
		diagLog.Errorf("startup: %v", err)
	}
	cancelRecovery()

	catalogClient := catalog.NewCatalogClient()
	defer catalogClient.Close()
	dependencies, err := newAPIDependencies(opts, store, filesMgr, secretsMgr, inspector, runtimeClient, deployCoord, catalogClient)
	if err != nil {
		return err
	}
	checksCtx, stopChecks := context.WithCancel(context.Background())
	checksDone := make(chan struct{})
	go func() { defer close(checksDone); dependencies.Plugins.RunCatalogChecks(checksCtx) }()
	defer func() { stopChecks(); <-checksDone }()
	deployCoord.SetBouncerVerifier(dependencies.CrowdSec)
	dependencies.Web = webHandler
	handler, err := api.NewServer(dependencies, middleware.Policy{
		AllowedHost: opts.ConsoleHost, AllowedOrigin: opts.ConsoleOrigin, ApplyTimeout: timeoutBudget.Apply,
		Gate: newAuthGate(opts, authSvc).Wrap,
	})
	if err != nil {
		return fmt.Errorf("API composition: %w", err)
	}

	srv := &http.Server{
		Addr:              net.JoinHostPort(opts.BindAddress, opts.Port),
		Handler:           handler,
		MaxHeaderBytes:    16 * 1024,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      timeoutBudget.HTTPWrite,
		IdleTimeout:       60 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)

	serveErrors := make(chan error, 1)
	go func() {
		diagLog.Infof("traefik console listening on http://%s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrors <- err
		}
	}()

	select {
	case <-stop:
	case err := <-serveErrors:
		return fmt.Errorf("HTTP server: %w", err)
	}
	diagLog.Infof("shutting down gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("server shutdown: %w", err)
	}
	diagLog.Infof("server stopped")

	return nil
}

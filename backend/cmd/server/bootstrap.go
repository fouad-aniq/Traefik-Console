package main

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/jmoiron/sqlx"

	"github.com/traefik-console/internal/config"
	"github.com/traefik-console/internal/config/filesystem"
	"github.com/traefik-console/internal/config/secrets"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/docker"
)

// loadOptions has no persistent effects. Reject settings/transport errors before
// opening state or generating a key; healthcheck and validation do not call it.
func loadOptions() (*config.Options, *docker.Client, error) {
	opts, e := config.Load()
	if e != nil {
		return nil, nil, e
	}
	runtime, e := docker.New(docker.Registration{DockerHost: opts.Docker.Host, TraefikContainer: opts.Docker.TraefikContainer, CrowdSecContainer: opts.Docker.CrowdSecContainer, SecretsDir: opts.RuntimeSecretsDir, AllowRestart: opts.Docker.AllowRestart})
	if e != nil {
		return nil, nil, e
	}
	return opts, runtime, nil
}

// stateResources owns only persistence resources and their lifetime. It is local
// to startup, never passed to application components and has no service getters.
type stateResources struct {
	store    db.Store
	key      []byte
	database *sqlx.DB
	lease    *os.File
}

func openState(opts *config.Options) (*stateResources, error) {
	if err := filesystem.EnsureDir(filepath.Dir(opts.DBPath)); err != nil {
		return nil, err
	}
	lease, err := filesystem.AcquireLease(opts.DBPath + ".lock")
	if err != nil {
		return nil, err
	}
	state := &stateResources{lease: lease}
	success := false
	defer func() {
		if !success {
			_ = state.Close()
		}
	}()
	_, e := os.Lstat(opts.DBPath)
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	existing := e == nil
	if err = filesystem.EnsureDir(filepath.Dir(opts.MasterKeyFile)); err != nil {
		return nil, err
	}
	state.key, err = secrets.LoadMasterKey(opts.MasterKeyFile, existing)
	if err != nil {
		return nil, err
	}
	state.database, err = db.InitDB(opts.DBPath)
	if err != nil {
		return nil, err
	}
	state.store = db.NewStore(state.database, state.key, opts.JournalLimitBytes, opts.SnapshotLimitBytes)
	success = true
	return state, nil
}
func (s *stateResources) Close() error {
	var errs []error
	if s.database != nil {
		errs = append(errs, s.database.Close())
		s.database = nil
	}
	if s.lease != nil {
		errs = append(errs, s.lease.Close())
		s.lease = nil
	}
	return errors.Join(errs...)
}

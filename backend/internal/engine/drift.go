package engine

import (
	"context"
	"time"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/docker"
	"github.com/traefik-console/internal/models"
)

func filesErrConflict() error { return files.ErrResourceConflict }

type published struct{ static, dynamic, domains string }

func (c *operations) readPublished(ctx context.Context) (published, error) {
	var p published
	var e error
	if p.static, e = c.store.GetSetting(ctx, settingPublishedStatic); e != nil {
		return p, e
	}
	if p.dynamic, e = c.store.GetSetting(ctx, settingPublishedDynamic); e != nil {
		return p, e
	}
	p.domains, e = c.store.GetSetting(ctx, settingPublishedDomains)
	return p, e
}

// driftedFiles names the managed files whose hash differs from the last published
// one. A file with no recorded baseline is never reported (fresh or pre-upgrade installs).
func driftedFiles(pub published, d diskFiles) []string {
	sh, dh, domh := d.hashes()
	var out []string
	if pub.static != "" && pub.static != sh {
		out = append(out, "traefik.yml")
	}
	if pub.dynamic != "" && pub.dynamic != dh {
		out = append(out, "dynamic")
	}
	if pub.domains != "" && pub.domains != domh {
		out = append(out, "domains")
	}
	return out
}

// checkDriftLocked refuses to write over files somebody changed outside the console.
func (c *operations) checkDriftLocked(ctx context.Context, d diskFiles) error {
	pub, e := c.readPublished(ctx)
	if e != nil {
		return e
	}
	if len(driftedFiles(pub, d)) > 0 {
		return ErrConfigDrift
	}
	return nil
}

// SeedPublishedHashes records the current file hashes when no baseline exists yet,
// so drift detection starts clean after the upgrade.
func (c *operations) SeedPublishedHashes(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seedPublishedHashesLocked(ctx)
}

func (c *operations) seedPublishedHashesLocked(ctx context.Context) error {
	pub, e := c.readPublished(ctx)
	if e != nil {
		return e
	}
	if pub.static != "" && pub.dynamic != "" && pub.domains != "" {
		return nil
	}
	d, e := c.readFiles()
	if e != nil {
		return e
	}
	sh, dh, domh := d.hashes()
	for _, s := range []struct{ key, have, want string }{{settingPublishedStatic, pub.static, sh}, {settingPublishedDynamic, pub.dynamic, dh}, {settingPublishedDomains, pub.domains, domh}} {
		if s.have == "" {
			if e = c.store.SetSetting(ctx, s.key, s.want); e != nil {
				return e
			}
		}
	}
	return nil
}

// State implements GET /config/state. It never waits for a running operation.
func (c *operations) State(ctx context.Context) (*models.ConfigState, error) {
	state := &models.ConfigState{}
	drafts, secretDrafts, e := c.store.CountLegacyArchive(ctx)
	if e != nil {
		return nil, e
	}
	state.ArchivedDrafts, state.ArchivedSecretDrafts = drafts, secretDrafts
	if v, _ := c.store.GetSetting(ctx, "legacy_snapshots_imported"); v == "1" {
		state.LegacySnapshotsImport = 1
	}
	recovery, e := c.store.RecoveryOperations(ctx)
	if e != nil {
		return nil, e
	}
	if len(recovery) > 0 {
		op := recovery[0]
		op.PriorState, op.PriorStatic, op.PriorDynamic, op.PriorDomains = nil, nil, nil, nil
		state.RecoveryOperation = &op
	}
	pub, e := c.readPublished(ctx)
	if e != nil {
		return nil, e
	}
	state.PublishedStaticHash, state.PublishedDynamicHash, state.PublishedDomainsHash = pub.static, pub.dynamic, pub.domains
	state.RestartRequired = c.restartPending(ctx)

	if !c.mu.TryLock() {
		state.RunningOperation = true
		return state, nil
	}
	defer c.mu.Unlock()
	d, e := c.readFiles()
	if e != nil {
		return nil, e
	}
	state.StaticHash, state.DynamicHash, state.DomainsHash = d.hashes()
	state.DriftedFiles = driftedFiles(pub, d)
	state.Drifted = len(state.DriftedFiles) > 0
	return state, nil
}

// restartPending reports a saved static change that Traefik has not loaded yet. It clears
// itself when the container started after the change was saved.
func (c *operations) restartPending(ctx context.Context) bool {
	since, e := c.store.GetSetting(ctx, settingRestartPending)
	if e != nil || since == "" {
		return false
	}
	saved, e := time.Parse(time.RFC3339, since)
	if e != nil {
		return true
	}
	inspector, ok := c.runtime.(docker.TraefikContainerInspector)
	if !ok {
		return true
	}
	status, e := inspector.InspectTraefikContainer(ctx)
	if e == nil && status != nil && status.Running && status.StartedAt.After(saved) {
		_ = c.store.SetSetting(ctx, settingRestartPending, "")
		return false
	}
	return true
}

// Adopt accepts the files as they are: it snapshots them (origin adopted) and records their hashes.
func (c *operations) Adopt(ctx context.Context) (*models.SnapshotRow, error) {
	if !c.mu.TryLock() {
		return nil, ErrDeployInProgress
	}
	defer c.mu.Unlock()
	if blocked, e := c.recoveryBlocked(ctx); e != nil {
		return nil, e
	} else if blocked {
		return nil, ErrOperationRecoveryRequired
	}
	d, e := c.readFiles()
	if e != nil {
		return nil, e
	}
	active, e := c.store.CaptureState(ctx)
	if e != nil {
		return nil, e
	}
	snap, e := c.createSnapshotLocked(ctx, snapshotInput{name: "Adopted files " + time.Now().UTC().Format("2006-01-02 15:04"), description: "Files changed outside the console were accepted as the new baseline.", origin: models.SnapshotAdopted, state: active, files: d})
	if e != nil {
		return nil, e
	}
	sh, dh, domh := d.hashes()
	for key, value := range map[string]string{settingPublishedStatic: sh, settingPublishedDynamic: dh, settingPublishedDomains: domh} {
		if e = c.store.SetSetting(ctx, key, value); e != nil {
			return nil, e
		}
	}
	return snap, nil
}

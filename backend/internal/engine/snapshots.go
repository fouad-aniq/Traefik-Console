package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/traefik-console/internal/config/yaml"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/redaction"
)

// Snapshots are user-made restore points: the full console state plus the exact
// bytes of the three managed files. Creating one never calls Traefik's write path.
type Snapshots interface {
	CreateSnapshot(ctx context.Context, name, description string) (*models.SnapshotRow, error)
	UpdateSnapshot(context.Context, int64, models.SnapshotPatch) (*models.SnapshotRow, error)
	DeleteSnapshot(ctx context.Context, id int64, force bool) error
	PreviewRestore(context.Context, int64) (*models.OperationPreview, error)
	RestoreSnapshot(context.Context, int64, models.RestoreRequest) (*models.RestoreResult, error)
	SnapshotConfig(context.Context, int64) (*models.SnapshotConfig, error)
	SnapshotResources(context.Context, int64) ([]models.SnapshotResource, error)
	CompareSnapshots(ctx context.Context, from, to int64) (*models.OperationPreview, error)
}

type snapshotInput struct {
	name, description, origin string
	state                     *db.State
	files                     diskFiles
	sourceOperation           *int64
	knownGood                 bool
}

var automaticOrigins = map[string]bool{models.SnapshotPreRestore: true, models.SnapshotPreStatic: true, models.SnapshotScheduled: true, models.SnapshotAdopted: true}

// createSnapshotLocked seals state and files into a snapshot. The caller holds the
// operation lock and has already decided drift is acceptable.
func (c *operations) createSnapshotLocked(ctx context.Context, in snapshotInput) (*models.SnapshotRow, error) {
	stateBytes, e := json.Marshal(&db.State{Items: in.state.Items, Secrets: in.state.Secrets})
	if e != nil {
		return nil, e
	}
	if len(in.name) > 200 {
		in.name = in.name[:200]
	}
	sh, dh, domh := in.files.hashes()
	row := &models.SnapshotRow{
		Name: in.name, Description: in.description, Origin: in.origin, KnownGood: in.knownGood,
		StaticYAML: in.files.static, DynamicYAML: in.files.dynamic, DomainsYAML: in.files.domains, State: stateBytes,
		StaticHash: sh, DynamicHash: dh, DomainsHash: domh, StateHash: db.HashBytes(stateBytes),
		ItemCount: len(in.state.Items), SourceOperationID: in.sourceOperation,
	}
	_, e = c.store.CreateSnapshot(ctx, row)
	if errors.Is(e, db.ErrSnapshotQuota) && automaticOrigins[in.origin] {
		// Automatic snapshots make room for themselves before a quota error blocks a safety net.
		if _, pe := c.store.PruneAutomaticSnapshots(ctx, 0); pe == nil {
			_, e = c.store.CreateSnapshot(ctx, row)
		}
	}
	if e != nil {
		return nil, e
	}
	if automaticOrigins[in.origin] && c.opts.AutomaticSnapshotRetention >= 0 {
		if _, pe := c.store.PruneAutomaticSnapshots(ctx, c.opts.AutomaticSnapshotRetention); pe != nil {
			log.Printf("WARN automatic snapshot pruning failed: %v", pe)
		}
	}
	row.StaticYAML, row.DynamicYAML, row.DomainsYAML, row.State = nil, nil, nil, nil
	return row, nil
}

// EnsureInitialSnapshot records the installation as it is on first start: when the console has no
// snapshot at all, it seals the current state and files as an "initial" restore point. It returns
// nil without error when a snapshot already exists, an operation needs recovery, or the files drifted
// (a baseline that does not match its state could not be restored safely). Like a manual snapshot,
// the initial one is never pruned.
func (c *operations) EnsureInitialSnapshot(ctx context.Context) (*models.SnapshotRow, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	existing, e := c.store.ListSnapshots(ctx, 1, 0)
	if e != nil {
		return nil, e
	}
	if len(existing) > 0 {
		return nil, nil
	}
	if blocked, e := c.recoveryBlocked(ctx); e != nil {
		return nil, e
	} else if blocked {
		return nil, nil
	}
	d, e := c.readFiles()
	if e != nil {
		return nil, e
	}
	if e = c.checkDriftLocked(ctx, d); e != nil {
		log.Printf("WARN initial snapshot skipped: managed files drifted; adopt them to record a baseline")
		return nil, nil
	}
	state, e := c.store.CaptureState(ctx)
	if e != nil {
		return nil, e
	}
	return c.createSnapshotLocked(ctx, snapshotInput{
		name:        "Initial configuration",
		description: "Taken automatically on first start, before any change made through the console.",
		origin:      models.SnapshotInitial, state: state, files: d, knownGood: c.probeKnownGood(ctx, d.dynamic),
	})
}

// probeKnownGood is true when Traefik currently serves the managed dynamic file without errors.
// It is advisory (a snapshot is created even when Traefik is down) and bounded to a few seconds.
func (c *operations) probeKnownGood(ctx context.Context, dynamic []byte) bool {
	if c.inspector == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	node, _, e := parsePair(dynamic, dynamic)
	if e != nil {
		return false
	}
	outcome, e := c.inspector.VerifyConvergenceWithRetry(ctx, node, node, 2*time.Second)
	return e == nil && outcome.Loaded && !outcome.AttributableErr
}

func (c *operations) CreateSnapshot(ctx context.Context, name, description string) (*models.SnapshotRow, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 || len(description) > 2000 {
		return nil, ErrValidationFailed
	}
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
	// A snapshot whose files do not match its state cannot be restored safely: refuse until adopted.
	if e = c.checkDriftLocked(ctx, d); e != nil {
		return nil, e
	}
	state, e := c.store.CaptureState(ctx)
	if e != nil {
		return nil, e
	}
	return c.createSnapshotLocked(ctx, snapshotInput{name: name, description: description, origin: models.SnapshotManual, state: state, files: d, knownGood: c.probeKnownGood(ctx, d.dynamic)})
}

func (c *operations) UpdateSnapshot(ctx context.Context, id int64, p models.SnapshotPatch) (*models.SnapshotRow, error) {
	if p.Name != nil {
		trimmed := strings.TrimSpace(*p.Name)
		if trimmed == "" || len(trimmed) > 200 {
			return nil, ErrValidationFailed
		}
		p.Name = &trimmed
	}
	if p.Description != nil && len(*p.Description) > 2000 {
		return nil, ErrValidationFailed
	}
	row, e := c.store.UpdateSnapshot(ctx, id, p)
	if e != nil {
		return nil, e
	}
	if row == nil {
		return nil, ErrSnapshotNotFound
	}
	return row, nil
}

func (c *operations) DeleteSnapshot(ctx context.Context, id int64, force bool) error {
	meta, e := c.store.GetSnapshotMeta(ctx, id)
	if e != nil {
		return e
	}
	if meta == nil {
		return ErrSnapshotNotFound
	}
	if meta.Pinned && !force {
		return ErrSnapshotPinned
	}
	return c.store.DeleteSnapshot(ctx, id)
}

func (c *operations) loadSnapshot(ctx context.Context, id int64) (*models.SnapshotRow, *db.State, error) {
	snap, e := c.store.GetSnapshot(ctx, id)
	if e != nil {
		return nil, nil, e
	}
	if snap == nil || len(snap.State) == 0 {
		return nil, nil, ErrSnapshotNotFound
	}
	var state db.State
	if json.Unmarshal(snap.State, &state) != nil {
		return nil, nil, ErrSnapshotNotFound
	}
	return snap, &state, nil
}

// restoreMutation turns a snapshot into the mutation that makes the committed state equal to it.
func restoreMutation(before *db.State, snap *models.SnapshotRow, target *db.State) (models.Mutation, error) {
	m := models.Mutation{Kind: models.OpRestore, Target: "snapshot:" + itoa(snap.ID), Summary: "Restore snapshot " + itoa(snap.ID) + ": " + snap.Name}
	wanted := map[string]bool{}
	for _, row := range target.Items {
		if len(row.Payload) == 0 {
			continue
		}
		wanted[itemKey(row.Kind, row.Name)] = true
		if row.Kind == models.KindCrowdSec {
			// A restored integration re-registers its bouncer (the registration may be gone).
			var cfg map[string]json.RawMessage
			if json.Unmarshal(row.Payload, &cfg) != nil {
				return m, ErrValidationFailed
			}
			var enabled bool
			var ids []int64
			var machineEnrollmentID int64
			_ = json.Unmarshal(cfg["enabled"], &enabled)
			_ = json.Unmarshal(cfg["enrollment_ids"], &ids)
			_ = json.Unmarshal(cfg["machine_enrollment_id"], &machineEnrollmentID)
			if enabled && len(ids) > 0 {
				cfg["auto_enroll"] = json.RawMessage("true")
			}
			if enabled && machineEnrollmentID > 0 {
				cfg["auto_enroll_machine"] = json.RawMessage("true")
			}
			row.Payload, _ = json.Marshal(cfg)
		}
		m.Upserts = append(m.Upserts, row)
	}
	for _, row := range before.Items {
		if !wanted[itemKey(row.Kind, row.Name)] {
			m.Deletes = append(m.Deletes, models.ItemRef{Kind: row.Kind, Name: row.Name})
		}
	}
	kept := map[string]bool{}
	for _, s := range target.Secrets {
		if len(s.Active) == 0 {
			continue
		}
		row := s.Metadata
		row.EncryptedValue = s.Active
		kept[row.Name] = true
		m.Secrets = append(m.Secrets, row)
	}
	for _, s := range before.Secrets {
		if !kept[s.Metadata.Name] {
			m.SecretDeletes = append(m.SecretDeletes, s.Metadata.Name)
		}
	}
	return m, nil
}

func itoa(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// restorePlan prepares (without side effects) the operation that restores a snapshot.
func (c *operations) restorePlan(ctx context.Context, id int64) (*plan, *models.SnapshotRow, error) {
	snap, target, e := c.loadSnapshot(ctx, id)
	if e != nil {
		return nil, nil, e
	}
	before, e := c.store.CaptureState(ctx)
	if e != nil {
		return nil, nil, e
	}
	m, e := restoreMutation(before, snap, target)
	if e != nil {
		return nil, nil, e
	}
	p, e := c.prepare(ctx, m, snap, false)
	if e != nil {
		return nil, nil, e
	}
	// Resources the restore removes may still be used by files the console does not own: warn.
	if re := c.protectExternalReferences(m.Deletes); re != nil {
		p.validation.Warnings = append(p.validation.Warnings, models.ValidationError{ItemID: "restore", Field: "EXTERNAL_REFERENCE", Message: "A resource this restore removes is referenced by configuration the console does not own"})
	}
	return p, snap, nil
}

// restoreToken binds a restore to the snapshot, the current files and the current console state.
func restoreToken(snap *models.SnapshotRow, p *plan) (string, error) {
	state, e := json.Marshal(p.before)
	if e != nil {
		return "", e
	}
	sh, dh, domh := p.files.hashes()
	sum := sha256.Sum256([]byte(itoa(snap.ID) + "|" + snap.StateHash + "|" + sh + "|" + dh + "|" + domh + "|" + db.HashBytes(state)))
	return hex.EncodeToString(sum[:]), nil
}

func resourceDiff(from, to []models.ConfigItemRow) (added, removed, changed []models.SnapshotResource) {
	old, next := rowsByKey(from), rowsByKey(to)
	for key, row := range next {
		prev, ok := old[key]
		switch {
		case !ok:
			added = append(added, models.SnapshotResource{Kind: row.Kind, Name: row.Name})
		case string(prev.Payload) != string(row.Payload):
			changed = append(changed, models.SnapshotResource{Kind: row.Kind, Name: row.Name})
		}
	}
	for key, row := range old {
		if _, ok := next[key]; !ok {
			removed = append(removed, models.SnapshotResource{Kind: row.Kind, Name: row.Name})
		}
	}
	for _, list := range [][]models.SnapshotResource{added, removed, changed} {
		sort.Slice(list, func(i, j int) bool {
			if list[i].Kind != list[j].Kind {
				return list[i].Kind < list[j].Kind
			}
			return list[i].Name < list[j].Name
		})
	}
	return
}

// PreviewRestore shows what a restore would change and returns the token the restore must present.
func (c *operations) PreviewRestore(ctx context.Context, id int64) (*models.OperationPreview, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if blocked, e := c.recoveryBlocked(ctx); e != nil {
		return nil, e
	} else if blocked {
		return nil, ErrOperationRecoveryRequired
	}
	p, snap, e := c.restorePlan(ctx, id)
	if e != nil {
		return nil, e
	}
	preview := p.preview()
	preview.Added, preview.Removed, preview.Changed = resourceDiff(p.before.Items, p.after.Items)
	if preview.RestoreToken, e = restoreToken(snap, p); e != nil {
		return nil, e
	}
	return preview, nil
}

// RestoreSnapshot restores a snapshot as one operation of kind restore.
func (c *operations) RestoreSnapshot(ctx context.Context, id int64, req models.RestoreRequest) (*models.RestoreResult, error) {
	if !c.mu.TryLock() {
		return nil, ErrDeployInProgress
	}
	defer c.mu.Unlock()
	if !requestKeyPattern.MatchString(req.IdempotencyKey) || len(req.RestoreToken) != 64 {
		return nil, ErrPreconditionRequired
	}
	hash := mutationHash(models.Mutation{Kind: models.OpRestore, Target: "snapshot:" + itoa(id), Summary: req.RestoreToken})
	if old, e := c.store.FindOperationByKey(ctx, req.IdempotencyKey); e != nil {
		return nil, e
	} else if old != nil {
		result, e := c.replay(ctx, old, hash)
		if e != nil {
			return nil, e
		}
		return &models.RestoreResult{Operation: result.Operation}, nil
	}
	if blocked, e := c.recoveryBlocked(ctx); e != nil {
		return nil, e
	} else if blocked {
		return nil, ErrOperationRecoveryRequired
	}
	p, snap, e := c.restorePlan(ctx, id)
	if e != nil {
		return nil, e
	}
	token, e := restoreToken(snap, p)
	if e != nil {
		return nil, e
	}
	if token != req.RestoreToken {
		return nil, ErrRestoreStale
	}
	for _, env := range p.validation.EnvVars {
		if !env.AlreadySet {
			return nil, ErrRecreateRequired
		}
	}
	result := &models.RestoreResult{}
	if !req.SkipBackup {
		// The backup must describe the current state exactly, which drifted files cannot.
		if e = c.checkDriftLocked(ctx, p.files); e != nil {
			return nil, e
		}
		backup, e := c.createSnapshotLocked(ctx, snapshotInput{name: "Before restoring #" + itoa(id) + ": " + snap.Name, origin: models.SnapshotPreRestore, state: p.before, files: p.files})
		if e != nil {
			return nil, e
		}
		result.BackupSnapshotID = &backup.ID
	}
	applied, e := c.publish(ctx, publication{plan: p, key: req.IdempotencyKey, mutateHash: hash, kind: models.OpRestore, target: p.m.Target, summary: p.m.Summary, warnings: p.validation.Warnings})
	if e != nil {
		return nil, e
	}
	result.Operation, result.Warnings = applied.Operation, applied.Warnings
	return result, nil
}

func (c *operations) SnapshotConfig(ctx context.Context, id int64) (*models.SnapshotConfig, error) {
	snap, e := c.store.GetSnapshot(ctx, id)
	if e != nil {
		return nil, e
	}
	if snap == nil {
		return nil, ErrSnapshotNotFound
	}
	return &models.SnapshotConfig{ID: snap.ID, StaticYAML: string(redaction.YAML(snap.StaticYAML)), DynamicYAML: string(redaction.YAML(snap.DynamicYAML)), DomainsYAML: string(redaction.YAML(snap.DomainsYAML))}, nil
}

func (c *operations) SnapshotResources(ctx context.Context, id int64) ([]models.SnapshotResource, error) {
	_, state, e := c.loadSnapshot(ctx, id)
	if e != nil {
		return nil, e
	}
	out := make([]models.SnapshotResource, 0, len(state.Items))
	for _, row := range state.Items {
		if len(row.Payload) > 0 {
			out = append(out, models.SnapshotResource{Kind: row.Kind, Name: row.Name})
		}
	}
	return out, nil
}

// CompareSnapshots answers "what changed between these two restore points".
func (c *operations) CompareSnapshots(ctx context.Context, from, to int64) (*models.OperationPreview, error) {
	a, as, e := c.loadSnapshot(ctx, from)
	if e != nil {
		return nil, e
	}
	b, bs, e := c.loadSnapshot(ctx, to)
	if e != nil {
		return nil, e
	}
	preview := &models.OperationPreview{
		Changes:     diffItems(as.Items, bs.Items),
		StaticDiff:  yaml.ComputeSimpleDiff(redaction.YAML(a.StaticYAML), redaction.YAML(b.StaticYAML)),
		DynamicDiff: yaml.ComputeSimpleDiff(redaction.YAML(a.DynamicYAML), redaction.YAML(b.DynamicYAML)),
		DomainDiff:  yaml.ComputeSimpleDiff(redaction.YAML(a.DomainsYAML), redaction.YAML(b.DomainsYAML)),
	}
	preview.RequiresRestart = db.HashBytes(a.StaticYAML) != db.HashBytes(b.StaticYAML)
	preview.Added, preview.Removed, preview.Changed = resourceDiff(as.Items, bs.Items)
	return preview, nil
}

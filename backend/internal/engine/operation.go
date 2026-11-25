package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/traefik-console/internal/config"
	"github.com/traefik-console/internal/config/yaml"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/redaction"
)

// Applier is the only way a resource manager writes: one mutation, one operation.
type Applier interface {
	Apply(context.Context, models.Mutation) (*models.OperationResult, error)
	// Preview runs the same pipeline up to the journal and changes nothing (?dry_run=true).
	Preview(context.Context, models.Mutation) (*models.OperationPreview, error)
}

// Write applies (or, for a dry run, previews) one mutation and shapes the response
// every resource manager returns. load re-reads the committed resource; nil for deletes.
func Write[T any](ctx context.Context, a Applier, m models.Mutation, opts models.WriteOptions, load func(context.Context) (*models.UIResource[T], error)) (*models.WriteResult[T], error) {
	if opts.DryRun {
		preview, e := a.Preview(ctx, m)
		if e != nil {
			return nil, e
		}
		return &models.WriteResult[T]{Preview: preview, Warnings: preview.ValidationWarnings}, nil
	}
	applied, e := a.Apply(ctx, m)
	if e != nil {
		return nil, e
	}
	out := &models.WriteResult[T]{Operation: applied.Operation, Warnings: applied.Warnings, RestartRequired: applied.RestartRequired()}
	if load != nil {
		if out.Resource, e = load(ctx); e != nil {
			return nil, e
		}
	}
	return out, nil
}

// Operations is the operation engine. Every create/update/delete/restore is one
// serialized operation: validate, render, journal, publish, verify, commit.
type Operations interface {
	Applier
	Snapshots
	Recover(context.Context, int64) (*models.OperationRow, error)
	Abandon(context.Context, int64) (*models.OperationRow, error)
	RecoverOnStartup(context.Context) error
	Restart(context.Context) error
	CurrentDiskHashes() (static, dynamic, domains string, err error)
	// State reports drift, restart_required and recovery status (GET /config/state).
	State(context.Context) (*models.ConfigState, error)
	// Adopt accepts drifted files as the new baseline (POST /config/adopt).
	Adopt(context.Context) (*models.SnapshotRow, error)
	// SeedPublishedHashes records the current file hashes when no baseline exists yet.
	SeedPublishedHashes(context.Context) error
	// EnsureInitialSnapshot creates the first-run baseline snapshot when no snapshot exists yet.
	EnsureInitialSnapshot(context.Context) (*models.SnapshotRow, error)
	SetBouncerVerifier(BouncerVerifier)
}

// plan is one mutation fully resolved against the committed state and rendered, still in memory.
type plan struct {
	m            models.Mutation
	files        diskFiles
	before       *db.State
	after        *db.State
	bundle       *GeneratedBundle
	domainsAfter []byte
	validation   *Validation
	changes      []models.ItemChange
	restore      *models.SnapshotRow
	secretsDiff  map[string]bool // names whose ciphertext changed or is new
}

func secretRows(states []db.SecretState) []models.SecretRow {
	rows := make([]models.SecretRow, 0, len(states))
	for _, s := range states {
		row := s.Metadata
		row.EncryptedValue = s.Active
		rows = append(rows, row)
	}
	return rows
}

// Dashboard machine passwords stay encrypted in Console state. Unlike the
// bouncer key, they are never published into Traefik's secret mount.
func runtimeSecret(row models.SecretRow) bool {
	return row.Purpose != "crowdsec_machine" && !strings.HasPrefix(row.Purpose, "crowdsec_manager")
}

func runtimeSecretRows(rows []models.SecretRow) []models.SecretRow {
	out := make([]models.SecretRow, 0, len(rows))
	for _, row := range rows {
		if runtimeSecret(row) {
			out = append(out, row)
		}
	}
	return out
}

func (p *plan) afterSecretRows() []models.SecretRow { return secretRows(p.after.Secrets) }

// secretWrites returns the secret files to write and the secret files to remove.
func (p *plan) secretWrites() ([]models.SecretRow, []string) {
	if p.restore == nil {
		deleted := make([]string, 0, len(p.m.SecretDeletes))
		for _, name := range p.m.SecretDeletes {
			runtime := true
			for _, prior := range p.before.Secrets {
				if prior.Metadata.Name == name && !runtimeSecret(prior.Metadata) {
					runtime = false
					break
				}
			}
			if runtime {
				deleted = append(deleted, name)
			}
		}
		return runtimeSecretRows(p.m.Secrets), deleted
	}
	kept := map[string]bool{}
	for _, s := range p.after.Secrets {
		kept[s.Metadata.Name] = true
	}
	var removed []string
	for _, s := range p.before.Secrets {
		if !kept[s.Metadata.Name] && runtimeSecret(s.Metadata) {
			removed = append(removed, s.Metadata.Name)
		}
	}
	return runtimeSecretRows(p.afterSecretRows()), removed
}

func (p *plan) secretNames() []string {
	set := map[string]bool{}
	for _, s := range p.before.Secrets {
		if runtimeSecret(s.Metadata) {
			set[s.Metadata.Name] = true
		}
	}
	for _, s := range p.after.Secrets {
		if runtimeSecret(s.Metadata) {
			set[s.Metadata.Name] = true
		}
	}
	for _, n := range p.m.SecretDeletes {
		for _, prior := range p.before.Secrets {
			if prior.Metadata.Name == n && runtimeSecret(prior.Metadata) {
				set[n] = true
				break
			}
		}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (p *plan) secretChanged(row models.SecretRow) bool { return p.secretsDiff[row.Name] }

// setIntegration replaces the CrowdSec integration payload with its final value (bouncer registered).
func (p *plan) setIntegration(payload json.RawMessage) {
	if i := integrationUpsert(&p.m); i >= 0 {
		p.m.Upserts[i].Payload = payload
	}
	for i := range p.after.Items {
		if p.after.Items[i].Kind == models.KindCrowdSec && p.after.Items[i].Name == "integration" {
			p.after.Items[i].Payload = payload
		}
	}
}

// mutationHash fingerprints a mutation for idempotent replays. Secret ciphertext is
// excluded on purpose: every encryption uses a fresh nonce, so re-sending the same
// request would otherwise never match its own stored operation.
func mutationHash(m models.Mutation) string {
	type item struct {
		Kind    models.ItemKind
		Name    string
		Payload json.RawMessage
	}
	type secret struct{ Name, Purpose, Env, Path string }
	doc := struct {
		Kind          models.OperationKind
		Upserts       []item
		Deletes       []models.ItemRef
		Secrets       []secret
		SecretDeletes []string
		Expected      map[string]int64
	}{Kind: m.Kind, Deletes: m.Deletes, SecretDeletes: m.SecretDeletes, Expected: m.ExpectedVersions}
	for _, u := range m.Upserts {
		doc.Upserts = append(doc.Upserts, item{u.Kind, u.Name, u.Payload})
	}
	for _, s := range m.Secrets {
		doc.Secrets = append(doc.Secrets, secret{s.Name, s.Purpose, s.EnvVarName, s.FilePath})
	}
	raw, _ := json.Marshal(doc)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// normalize fills the operation kind and target from the mutation's shape.
func normalize(m *models.Mutation) {
	if m.Kind == "" {
		switch {
		case len(m.Upserts) == 1 && len(m.Deletes) == 0 && len(m.Secrets) == 0 && len(m.SecretDeletes) == 0:
			m.Kind = models.OpUpsert
		case len(m.Upserts) == 0 && len(m.Deletes) == 1:
			m.Kind = models.OpDelete
		default:
			m.Kind = models.OpBatch
		}
	}
	if m.Target == "" {
		switch {
		case len(m.Upserts) == 1 && len(m.Deletes) == 0:
			m.Target = itemKey(m.Upserts[0].Kind, m.Upserts[0].Name)
		case len(m.Upserts) == 0 && len(m.Deletes) == 1:
			m.Target = m.Deletes[0].Key()
		}
	}
	for i := range m.Upserts {
		if m.Upserts[i].Scope == "" {
			m.Upserts[i].Scope = defaultScope(m.Upserts[i].Kind)
		}
	}
}

func defaultScope(kind models.ItemKind) models.ItemScope {
	if kind == models.KindResolver || kind == models.KindPlugin || kind == models.KindEntryPoint {
		return models.ScopeStatic
	}
	return models.ScopeDynamic
}

func summarize(changes []models.ItemChange) string {
	if len(changes) == 0 {
		return "Configuration sync"
	}
	parts := make([]string, 0, len(changes))
	for _, ch := range changes {
		if ch.Action == models.ChangeImpacted {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s %s", ch.Action, ch.Kind, ch.Name))
	}
	if len(parts) == 0 {
		return "Configuration sync"
	}
	if len(parts) > 6 {
		parts = append(parts[:6], fmt.Sprintf("and %d more", len(changes)-6))
	}
	return strings.Join(parts, ", ")
}

// prepare resolves a mutation against the committed state and renders it. It has
// no side effects, so Preview (dry run) and Apply share it exactly.
func (c *operations) prepare(ctx context.Context, m models.Mutation, restore *models.SnapshotRow, checkDrift bool) (*plan, error) {
	before, e := c.store.CaptureState(ctx)
	if e != nil {
		return nil, e
	}
	files, e := c.readFiles()
	if e != nil {
		return nil, e
	}
	if checkDrift {
		if e = c.checkDriftLocked(ctx, files); e != nil {
			return nil, e
		}
	}
	if restore == nil {
		if e = checkVersions(m, rowsByKey(before.Items)); e != nil {
			return nil, e
		}
		if e = resolveMarkers(&m, before); e != nil {
			return nil, e
		}
		if e = c.validateMutation(ctx, m, before); e != nil {
			return nil, e
		}
		if e = c.protectExternalReferences(m.Deletes); e != nil {
			return nil, e
		}
	}
	after, e := buildCandidate(before, m)
	if e != nil {
		return nil, e
	}
	p := &plan{m: m, files: files, before: before, after: after, restore: restore, secretsDiff: map[string]bool{}}

	if restore != nil {
		bd, ad, e := parsePair(files.dynamic, restore.DynamicYAML)
		if e != nil {
			return nil, e
		}
		p.bundle = &GeneratedBundle{StaticYAML: restore.StaticYAML, DynamicYAML: restore.DynamicYAML, BeforeDynNode: bd, AfterDynNode: ad, RequiresRestart: db.HashBytes(files.static) != db.HashBytes(restore.StaticYAML)}
		p.domainsAfter = restore.DomainsYAML
	} else {
		if p.bundle, e = c.builder.Render(after.Items, before.Items, files.static, files.dynamic); e != nil {
			return nil, e
		}
		if p.domainsAfter, e = renderDomains(after.Items); e != nil {
			return nil, e
		}
	}
	beforeSecrets := map[string][]byte{}
	for _, s := range before.Secrets {
		beforeSecrets[s.Metadata.Name] = s.Active
	}
	changedSecrets, _ := p.secretWrites()
	for _, s := range changedSecrets {
		old, existed := beforeSecrets[s.Name]
		same := false
		if existed {
			if same, e = c.secrets.SameValue(old, s.EncryptedValue); e != nil {
				return nil, e
			}
		}
		if !same {
			p.secretsDiff[s.Name] = true
		}
	}

	if e = c.validateEffectiveAgainst(ctx, after.Items, p.afterSecretRows(), p.bundle.StaticYAML); e != nil {
		return nil, e
	}
	if p.validation, e = c.builder.Validate(ctx, ValidationInput{Bundle: p.bundle, Candidate: after.Items, Previous: before.Items, Secrets: p.afterSecretRows()}); e != nil {
		return nil, e
	}
	if restore != nil {
		// A snapshot may reference something outside the console that is gone now: warn, do not block.
		p.validation.Warnings = append(p.validation.Warnings, p.validation.Errors...)
		p.validation.Errors = nil
	}
	p.changes = diffItems(before.Items, after.Items)
	if restore == nil {
		derived, e := derivedRouterChanges(before.Items, after.Items, files.static, p.bundle.StaticYAML)
		if e != nil {
			return nil, e
		}
		p.changes = append(p.changes, derived...)
		sortChanges(p.changes)
	}
	return p, nil
}

func (p *plan) preview() *models.OperationPreview {
	staticBefore, dynamicBefore, domainsBefore := redaction.YAML(p.files.static), redaction.YAML(p.files.dynamic), redaction.YAML(p.files.domains)
	return &models.OperationPreview{
		Changes:            p.changes,
		RequiresRestart:    p.bundle.RequiresRestart,
		RestartReasons:     p.bundle.RestartReasons,
		EnvVarsToMount:     p.validation.EnvVars,
		ValidationErrors:   p.validation.Errors,
		ValidationWarnings: p.validation.Warnings,
		StaticDiff:         yaml.ComputeSimpleDiff(staticBefore, redaction.YAML(p.bundle.StaticYAML)),
		DynamicDiff:        yaml.ComputeSimpleDiff(dynamicBefore, redaction.YAML(p.bundle.DynamicYAML)),
		DomainDiff:         yaml.ComputeSimpleDiff(domainsBefore, redaction.YAML(p.domainsAfter)),
	}
}

// noChange reports whether publishing would change nothing at all.
func (p *plan) noChange() bool {
	if p.restore != nil || len(p.changes) > 0 || len(p.secretsDiff) > 0 || len(p.m.SecretDeletes) > 0 || p.bundle.RequiresRestart {
		return false
	}
	return string(p.files.dynamic) == string(p.bundle.DynamicYAML) && string(p.files.domains) == string(p.domainsAfter)
}

// Preview implements ?dry_run=true: the full pipeline minus the journal and every side effect.
func (c *operations) Preview(ctx context.Context, m models.Mutation) (*models.OperationPreview, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if blocked, e := c.recoveryBlocked(ctx); e != nil {
		return nil, e
	} else if blocked {
		return nil, ErrOperationRecoveryRequired
	}
	normalize(&m)
	applyContextVersion(ctx, &m)
	p, e := c.prepare(ctx, m, nil, true)
	if e != nil {
		return nil, e
	}
	return p.preview(), nil
}

// Apply runs one mutation as one operation. A busy engine answers ErrDeployInProgress.
func (c *operations) Apply(ctx context.Context, m models.Mutation) (*models.OperationResult, error) {
	if !c.mu.TryLock() {
		return nil, ErrDeployInProgress
	}
	defer c.mu.Unlock()
	normalize(&m)
	applyContextVersion(ctx, &m)
	key, supplied, e := requestKeyFrom(ctx)
	if e != nil {
		return nil, e
	}
	hash := mutationHash(m)
	if supplied {
		if old, e := c.store.FindOperationByKey(ctx, key); e != nil {
			return nil, e
		} else if old != nil {
			return c.replay(ctx, old, hash)
		}
	}
	if blocked, e := c.recoveryBlocked(ctx); e != nil {
		return nil, e
	} else if blocked {
		return nil, ErrOperationRecoveryRequired
	}
	p, e := c.prepare(ctx, m, nil, true)
	if e != nil {
		return nil, e
	}
	if len(p.validation.Errors) > 0 {
		return nil, &ValidationFailure{Errors: p.validation.Errors}
	}
	for _, env := range p.validation.EnvVars {
		if !env.AlreadySet {
			return nil, ErrRecreateRequired
		}
	}
	if p.noChange() {
		return &models.OperationResult{NoChange: true, Warnings: p.validation.Warnings}, nil
	}
	if e := c.ensureDNSEnv(p); e != nil {
		return nil, e
	}
	summary := p.m.Summary
	if summary == "" {
		summary = summarize(p.changes)
	}
	return c.publish(ctx, publication{plan: p, key: key, mutateHash: hash, kind: p.m.Kind, target: p.m.Target, summary: summary, warnings: p.validation.Warnings})
}

// ensureDNSEnv adds the credential variables Traefik reads from the Console-kept DNS env file; when
// the file changed, Traefik is restarted with this operation so it loads them.
func (c *operations) ensureDNSEnv(p *plan) error {
	w, ok := c.builder.(dnsEnvWriter)
	if !ok || p.validation == nil {
		return nil
	}
	changed, e := w.EnsureDNSEnv(p.validation.EnvVars)
	if e != nil {
		return fmt.Errorf("update %s: %w", config.DNSProvidersEnvFile, e)
	}
	if changed {
		p.bundle.RequiresRestart = true
	}
	return nil
}

// replay answers a repeated Idempotency-Key from the stored operation.
func (c *operations) replay(ctx context.Context, old *models.OperationRow, hash string) (*models.OperationResult, error) {
	if old.MutationHash != hash {
		return nil, filesErrConflict()
	}
	switch old.Status {
	case models.OpStatusSucceeded, models.OpStatusRestartRequired:
		return &models.OperationResult{Operation: old}, nil
	case models.OpStatusCleanupPending:
		if e := c.finishCleanup(ctx, old); e != nil {
			return nil, e
		}
		return &models.OperationResult{Operation: old}, nil
	case models.OpStatusRolledBack:
		return nil, &DeployFailure{OperationID: old.ID, Stage: old.Stage, ReasonCode: old.ReasonCode, Reason: old.ErrorLog, RolledBack: true, cause: ErrRollbackTriggered}
	default:
		return nil, ErrOperationRecoveryRequired
	}
}

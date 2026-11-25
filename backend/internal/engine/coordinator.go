package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	domainfile "github.com/traefik-console/internal/config/domains"
	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/config/yaml"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/docker"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
)

const (
	reasonRestartRequired = "MANUAL_RESTART_REQUIRED"

	settingPublishedStatic  = "published_static_hash"
	settingPublishedDynamic = "published_dynamic_hash"
	settingPublishedDomains = "published_domains_hash"
	settingRestartPending   = "restart_pending_since"
)

type ConvergenceObserver interface {
	VerifyConvergenceWithRetry(context.Context, *yamlNode, *yamlNode, time.Duration) (status.VerificationOutcome, error)
}

// Options are the operator-tunable behaviours of the operation engine.
type Options struct {
	// AutoSnapshotBeforeStatic takes a pre_static snapshot before a change that rewrites traefik.yml.
	// Off by default: a failed operation is rolled back from its journal without a snapshot.
	AutoSnapshotBeforeStatic bool
	// AutomaticSnapshotRetention is how many unpinned automatic snapshots are kept (0 = keep none beyond the newest write).
	AutomaticSnapshotRetention int
	// OperationHistoryDays is how long finished operations stay in the audit trail (0 = forever).
	OperationHistoryDays int
}

// DefaultOptions are the documented defaults (see config/env.go).
func DefaultOptions() Options {
	return Options{AutoSnapshotBeforeStatic: false, AutomaticSnapshotRetention: 20, OperationHistoryDays: 90}
}

const (
	defaultConvergenceTimeout = 15 * time.Second
	// Apply needs time for enrollment, secret delivery and a possible restart before verification.
	applyStageReserve = 60 * time.Second
	// Recovery can require a restart as well as a complete convergence window.
	recoveryStageReserve = 30 * time.Second
	responseReserve      = 15 * time.Second
)

// TimeoutBudget keeps the request, rollback and HTTP response envelopes consistent.
// Configuration currently caps convergence at 60 seconds, making these bounded at
// 120 seconds for apply, 90 seconds for recovery and 225 seconds for the response.
type TimeoutBudget struct {
	Apply     time.Duration
	Recovery  time.Duration
	HTTPWrite time.Duration
}

func NewTimeoutBudget(convergence time.Duration) TimeoutBudget {
	if convergence <= 0 {
		convergence = defaultConvergenceTimeout
	}
	apply := convergence + applyStageReserve
	recovery := convergence + recoveryStageReserve
	return TimeoutBudget{Apply: apply, Recovery: recovery, HTTPWrite: apply + recovery + responseReserve}
}

type operations struct {
	mu                 sync.Mutex
	store              db.Store
	files              ConfigFiles
	secrets            SecretRuntime
	builder            Generator
	inspector          ConvergenceObserver
	runtime            docker.TraefikContainerRuntime
	convergenceTimeout time.Duration
	timeouts           TimeoutBudget
	faultHook          func(string)
	bouncerVerifier    BouncerVerifier
	opts               Options
}

// NewOperations builds the operation engine: the only writer of resources, secrets and managed files.
func NewOperations(s db.Store, f ConfigFiles, se SecretRuntime, g Generator, o ConvergenceObserver, d docker.TraefikContainerRuntime, t time.Duration, opts ...Options) Operations {
	return newOperations(s, f, se, g, o, d, t, opts...)
}

func newOperations(s db.Store, f ConfigFiles, se SecretRuntime, g Generator, o ConvergenceObserver, d docker.TraefikContainerRuntime, t time.Duration, opts ...Options) *operations {
	if t <= 0 {
		t = defaultConvergenceTimeout
	}
	options := DefaultOptions()
	if len(opts) > 0 {
		options = opts[0]
	}
	return &operations{store: s, files: f, secrets: se, builder: g, inspector: o, runtime: d, convergenceTimeout: t, timeouts: NewTimeoutBudget(t), opts: options}
}

func (c *operations) recoveryContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), c.timeouts.Recovery)
}

func (c *operations) fault(point string) {
	if c.faultHook != nil {
		c.faultHook(point)
	}
}

// diskFiles is one consistent read of the three managed files.
type diskFiles struct {
	static, dynamic, domains []byte
}

func (d diskFiles) hashes() (string, string, string) {
	return db.HashBytes(d.static), db.HashBytes(d.dynamic), db.HashBytes(d.domains)
}

func (c *operations) readFiles() (diskFiles, error) {
	var d diskFiles
	var e error
	if d.static, e = c.files.ReadStaticConfig(); e != nil && !errors.Is(e, files.ErrSourceFileNotFound) {
		return d, e
	}
	if d.dynamic, e = c.files.ReadManagedDynamicConfig(); e != nil {
		return d, e
	}
	d.domains, e = c.files.ReadConsoleDomainsConfig()
	return d, e
}

func renderDomains(rows []models.ConfigItemRow) ([]byte, error) {
	b, e := domainfile.Render(rows)
	if e != nil {
		return nil, e
	}
	if len(b) > yaml.DefaultMaxYAMLSize {
		return nil, ErrValidationFailed
	}
	return b, nil
}

// publication is everything publish needs about one operation.
type publication struct {
	plan       *plan
	key        string
	mutateHash string
	kind       models.OperationKind
	target     string
	summary    string
	warnings   []models.ValidationError
}

// publish writes the candidate to disk, proves it, and commits it: steps 6-10 of the
// operation pipeline. The journal row is durable BEFORE the first side effect, and
// every later failure reverts exactly this operation.
func (c *operations) publish(ctx context.Context, pub publication) (*models.OperationResult, error) {
	p := pub.plan
	b := p.bundle
	state := &db.State{DomainFileTracked: true, DomainFileAfter: p.domainsAfter}

	// Secrets the operation leaves on disk and secrets it removes.
	flush, removed := p.secretWrites()
	names := p.secretNames()
	var e error
	if state.Runtime, e = c.secrets.CaptureRuntime(ctx, names); e != nil {
		return nil, e
	}
	state.AttemptRuntime = c.secrets.CandidateRuntime(runtimeSecretRows(p.afterSecretRows()))
	if e = c.enrollmentPlan(ctx, state, &p.m, pub.key); e != nil {
		return nil, e
	}
	prior, e := json.Marshal(state)
	if e != nil {
		return nil, e
	}

	var warnings = append([]models.ValidationError(nil), pub.warnings...)
	if b.RequiresRestart && c.opts.AutoSnapshotBeforeStatic && p.restore == nil {
		if _, se := c.createSnapshotLocked(ctx, snapshotInput{
			name: "Before static change: " + pub.summary, origin: models.SnapshotPreStatic, state: p.before, files: p.files,
		}); se != nil {
			log.Printf("WARN pre_static snapshot skipped: %v", se)
			warnings = append(warnings, models.ValidationError{ItemID: "snapshot", Field: "PRE_STATIC_SNAPSHOT_SKIPPED", Message: "Traefik will restart, but the automatic snapshot before this change could not be created"})
		}
	}

	op := &models.OperationRow{
		Kind: pub.kind, Target: pub.target, Status: models.OpStatusRunning, Summary: pub.summary, RequiresRestart: b.RequiresRestart,
		StaticYAML: b.StaticYAML, DynamicYAML: b.DynamicYAML, PriorStatic: p.files.static, PriorDynamic: p.files.dynamic, PriorDomains: p.files.domains,
		PriorState: prior, RequestKey: pub.key, MutationHash: pub.mutateHash, Stage: "journal_recheck",
	}
	if op.ID, e = c.store.CreateOperation(ctx, op); e != nil {
		return nil, e
	}
	// The row now exists; keep the decoded copies for recovery even though the store sealed its own.
	op.Journaled = true

	c.fault("journaled")
	stage := "journal_recheck"
	fail := func(cause error) (*models.OperationResult, error) {
		code, reason := safeFailureReason(cause)
		failure := &DeployFailure{OperationID: op.ID, Stage: stage, ReasonCode: code, Reason: reason, cause: cause}
		// Record the original cause before compensation can fail or the process exits.
		op.ErrorLog = fmt.Sprintf("Operation failed at %s: %s [%s].", stage, reason, code)
		op.Stage, op.ReasonCode = stage, code
		rctx, cancel := c.recoveryContext()
		defer cancel()
		_ = c.store.UpdateOperation(rctx, op.ID, models.OpStatusRunning, stage, code, op.ErrorLog)
		if re := c.restorePrior(rctx, op); re != nil {
			failure.RecoveryCode, _ = safeFailureReason(re)
		} else {
			failure.RolledBack = true
		}
		log.Printf("WARN operation_id=%d stage=%s reason_code=%s rollback_completed=%t", op.ID, stage, code, failure.RolledBack)
		return nil, failure
	}

	// Recheck immediately before side effects, AFTER the intent is durable.
	current, e := c.readFiles()
	if e != nil || !sameFiles(current, p.files) {
		return fail(files.ErrResourceConflict)
	}

	stage = "bouncer_registration"
	finalIntegration, e := c.registerBouncer(ctx, state, p.m.Secrets)
	if e != nil {
		return fail(e)
	}
	if finalIntegration != nil {
		p.setIntegration(finalIntegration)
	}

	runtimeUnverified := false
	stage = "secret_publication"
	if len(flush)+len(removed) > 0 {
		if e = c.secrets.FlushSecretsToDisk(ctx, flush, removed); e != nil {
			return fail(e)
		}
	}
	c.fault("secrets_written")
	stage = "secret_delivery_probe"
	for _, row := range flush {
		if !p.secretChanged(row) {
			continue
		}
		e = c.runtime.ProbeTraefikSecretFile(ctx, row.Name)
		if errors.Is(e, docker.ErrCapabilityDisabled) || errors.Is(e, docker.ErrRestartDisabled) {
			runtimeUnverified = true
		} else if e != nil {
			return fail(fmt.Errorf("%w: %w", ErrRecreateRequired, e))
		}
	}

	dynamicChanged := !bytes.Equal(p.files.dynamic, b.DynamicYAML)
	stage = "dynamic_publication"
	if dynamicChanged {
		if e = c.files.WriteManagedDynamicConfig(b.DynamicYAML); e != nil {
			return fail(e)
		}
	}
	c.fault("dynamic_written")

	stage = "domain_publication"
	if !bytes.Equal(p.files.domains, p.domainsAfter) {
		currentDomains, e := c.files.ReadConsoleDomainsConfig()
		if e != nil || !bytes.Equal(currentDomains, p.files.domains) {
			return fail(files.ErrResourceConflict)
		}
		if e = c.files.WriteConsoleDomainsConfig(p.domainsAfter); e != nil {
			return fail(e)
		}
	}
	c.fault("domains_written")

	restartFailed := false
	if b.RequiresRestart {
		stage = "static_publication"
		if e = c.files.WriteStaticConfig(b.StaticYAML); e != nil {
			return fail(e)
		}
		c.fault("static_written")
		stage = "traefik_restart"
		if _, e = c.runtime.RestartTraefikContainerAndConfirm(ctx); e != nil {
			// The files stay written and the change is committed: the operator restarts Traefik by hand.
			if ctx.Err() != nil {
				return fail(e)
			}
			restartFailed = true
			log.Printf("WARN operation_id=%d traefik restart did not complete; manual restart required", op.ID)
		}
	}

	if !restartFailed && (dynamicChanged || b.RequiresRestart) {
		stage = "runtime_verification"
		outcome, e := c.inspector.VerifyConvergenceWithRetry(ctx, b.BeforeDynNode, b.AfterDynNode, c.convergenceTimeout)
		if e != nil {
			return fail(e)
		}
		if outcome.AttributableErr {
			return fail(errRuntimeRejected)
		}
		if !outcome.Loaded {
			return fail(errConvergenceTimeout)
		}
	}
	c.fault("observed")

	stage = "database_commit"
	final, e := c.readFiles()
	if e != nil {
		return fail(e)
	}
	sh, dh, domh := final.hashes()
	settings := map[string]string{settingPublishedStatic: sh, settingPublishedDynamic: dh, settingPublishedDomains: domh}
	switch {
	case restartFailed:
		settings[settingRestartPending] = time.Now().UTC().Format(time.RFC3339)
	case b.RequiresRestart:
		settings[settingRestartPending] = ""
	}
	cleanup := len(state.CleanupEnrollmentIDs) > 0 || state.CleanupMachineEnrollmentID > 0
	op.Status = models.OpStatusSucceeded
	op.Stage, op.ReasonCode, op.ErrorLog = "", "", ""
	if restartFailed {
		op.Status, op.ReasonCode = models.OpStatusRestartRequired, reasonRestartRequired
		op.ErrorLog = ErrManualRestartRequired.Error()
	}
	finalStatus := op.Status
	if cleanup {
		op.Status = models.OpStatusCleanupPending
	}
	commit := db.Commit{Operation: op, Mutation: p.m, Settings: settings, KeepJournal: cleanup}
	if p.restore != nil {
		commit.Replace = p.after
		commit.Mutation = models.Mutation{}
	}
	written, e := c.store.CommitOperation(ctx, commit)
	if e != nil {
		return fail(e)
	}
	if runtimeUnverified {
		warnings = append(warnings, models.ValidationError{ItemID: "secrets", Field: "SECRET_DELIVERY_UNVERIFIED", Message: "Credential delivery to the Traefik container could not be verified"})
	}

	c.fault("finalized")
	if cleanup {
		if e = c.finishCleanup(ctx, op); e != nil {
			return &models.OperationResult{Operation: op, Items: written, Warnings: warnings}, e
		}
	}
	op.Status = finalStatus
	c.prune(ctx)
	return &models.OperationResult{Operation: op, Items: written, Warnings: warnings}, nil
}

// prune enforces the retention settings after a successful operation. It never fails the operation.
func (c *operations) prune(ctx context.Context) {
	if c.opts.OperationHistoryDays > 0 {
		if _, e := c.store.PruneOperations(ctx, c.opts.OperationHistoryDays); e != nil {
			log.Printf("WARN operation history pruning failed: %v", e)
		}
	}
	if c.opts.AutomaticSnapshotRetention >= 0 {
		if _, e := c.store.PruneAutomaticSnapshots(ctx, c.opts.AutomaticSnapshotRetention); e != nil {
			log.Printf("WARN automatic snapshot pruning failed: %v", e)
		}
	}
}

func sameFiles(a, b diskFiles) bool {
	return bytes.Equal(a.static, b.static) && bytes.Equal(a.dynamic, b.dynamic) && bytes.Equal(a.domains, b.domains)
}

// restorePrior reverts exactly one operation: files, runtime secrets, bouncer
// registration. The database is untouched until commit, so it has nothing to undo.
func (c *operations) restorePrior(ctx context.Context, op *models.OperationRow) error {
	if len(op.PriorState) == 0 {
		return c.recoveryFailed(ctx, op, "recovery")
	}
	var state db.State
	if json.Unmarshal(op.PriorState, &state) != nil {
		return c.recoveryFailed(ctx, op, "recovery")
	}
	priorDomains := op.PriorDomains
	if priorDomains == nil && state.DomainFileTracked {
		priorDomains = state.DomainFileBefore // journals written by the retired draft engine
	}
	current, e := c.readFiles()
	if e != nil {
		return c.recoveryFailed(ctx, op, "recovery")
	}
	sh, dh, domh := current.hashes()
	if (sh != db.HashBytes(op.PriorStatic) && sh != db.HashBytes(op.StaticYAML)) ||
		(dh != db.HashBytes(op.PriorDynamic) && dh != db.HashBytes(op.DynamicYAML)) ||
		(domh != db.HashBytes(priorDomains) && domh != db.HashBytes(state.DomainFileAfter)) {
		// Somebody else changed a file since the operation started: never overwrite their work.
		return c.recoveryFailed(ctx, op, "recovery")
	}
	if e = c.store.UpdateOperation(ctx, op.ID, models.OpStatusRestoring, op.Stage, op.ReasonCode, op.ErrorLog); e != nil {
		return e
	}
	if e = c.secrets.RestoreRuntime(ctx, state.Runtime, state.AttemptRuntime); e != nil {
		return c.recoveryFailed(ctx, op, "recovery")
	}
	if e = c.files.RestoreManagedDynamicConfig(op.PriorDynamic); e != nil {
		return c.recoveryFailed(ctx, op, "recovery")
	}
	restartNeeded := sh != db.HashBytes(op.PriorStatic) // the new traefik.yml was written
	if e = c.files.RestoreStaticConfig(op.PriorStatic); e != nil {
		return c.recoveryFailed(ctx, op, "recovery")
	}
	if e = c.files.RestoreConsoleDomainsConfig(priorDomains); e != nil {
		return c.recoveryFailed(ctx, op, "recovery")
	}
	if restartNeeded {
		if _, e = c.runtime.RestartTraefikContainerAndConfirm(ctx); e != nil {
			return c.recoveryFailed(ctx, op, "recovery")
		}
	}
	if restartNeeded || dh != db.HashBytes(op.PriorDynamic) {
		before, after, e := parsePair(op.DynamicYAML, op.PriorDynamic)
		if e != nil {
			return c.recoveryFailed(ctx, op, "recovery")
		}
		result, e := c.inspector.VerifyConvergenceWithRetry(ctx, before, after, c.convergenceTimeout)
		if e != nil || !result.Loaded || result.AttributableErr {
			return c.recoveryFailed(ctx, op, "recovery")
		}
	}
	if state.AttemptEnrollmentID != 0 {
		registrar, ok := c.runtime.(docker.BouncerRegistrar)
		if !ok {
			return c.recoveryFailed(ctx, op, "recovery")
		}
		if e = registrar.RevokeBouncer(ctx, state.AttemptEnrollmentID); e != nil {
			return c.recoveryFailed(ctx, op, "recovery")
		}
	}
	if state.AttemptMachineEnrollmentID != 0 {
		registrar, ok := c.runtime.(docker.MachineRegistrar)
		if !ok {
			return c.recoveryFailed(ctx, op, "recovery")
		}
		if e = registrar.RevokeMachine(ctx, state.AttemptMachineEnrollmentID); e != nil {
			return c.recoveryFailed(ctx, op, "recovery")
		}
	}
	if e = c.store.UpdateOperation(ctx, op.ID, models.OpStatusRolledBack, op.Stage, op.ReasonCode, op.ErrorLog); e != nil {
		return e
	}
	op.Status = models.OpStatusRolledBack
	return c.store.PruneOperationBlobs(ctx, op.ID)
}

// recoveryFailed marks one operation as needing an explicit Recover or Abandon.
func (c *operations) recoveryFailed(ctx context.Context, op *models.OperationRow, stage string) error {
	op.Status, op.Stage = models.OpStatusRecovery, stage
	_ = c.store.UpdateOperation(ctx, op.ID, models.OpStatusRecovery, stage, "RECOVERY_REQUIRED", op.ErrorLog)
	return ErrOperationRecoveryRequired
}

// recoveryBlocked reports whether an earlier operation still needs recovery.
func (c *operations) recoveryBlocked(ctx context.Context) (bool, error) {
	pending, e := c.store.RecoveryOperations(ctx)
	return len(pending) > 0, e
}

// RecoverOnStartup finishes or reverts every operation a crash left in flight.
func (c *operations) RecoverOnStartup(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	pending, e := c.store.PendingOperations(ctx)
	if e != nil {
		return e
	}
	var failed bool
	for i := range pending {
		var re error
		if pending[i].Status == models.OpStatusCleanupPending {
			re = c.finishCleanup(ctx, &pending[i])
		} else {
			re = c.restorePrior(ctx, &pending[i])
		}
		if re != nil {
			failed = true
		}
	}
	if e = c.initializeDomainFile(ctx); e != nil {
		return e
	}
	if e = c.seedPublishedHashesLocked(ctx); e != nil {
		return e
	}
	if failed {
		return fmt.Errorf("%w: startup recovery", ErrOperationRecoveryRequired)
	}
	return nil
}

// Recover retries the revert (or the cleanup) of an operation stuck in recovery_required.
func (c *operations) Recover(ctx context.Context, id int64) (*models.OperationRow, error) {
	if !c.mu.TryLock() {
		return nil, ErrDeployInProgress
	}
	defer c.mu.Unlock()
	op, e := c.store.GetOperation(ctx, id)
	if e != nil {
		return nil, e
	}
	if op == nil {
		return nil, ErrOperationNotFound
	}
	if op.Status != models.OpStatusRecovery {
		return nil, ErrOperationNotRecoverable
	}
	if op.Stage == "cleanup" {
		e = c.finishCleanup(ctx, op)
	} else {
		e = c.restorePrior(ctx, op)
	}
	if e != nil {
		return op, e
	}
	return c.store.GetOperation(ctx, id)
}

// Abandon accepts the files exactly as they are and clears recovery_required. The
// database keeps its committed state, so the console reports drift until the files are adopted.
func (c *operations) Abandon(ctx context.Context, id int64) (*models.OperationRow, error) {
	if !c.mu.TryLock() {
		return nil, ErrDeployInProgress
	}
	defer c.mu.Unlock()
	op, e := c.store.GetOperation(ctx, id)
	if e != nil {
		return nil, e
	}
	if op == nil {
		return nil, ErrOperationNotFound
	}
	if op.Status != models.OpStatusRecovery {
		return nil, ErrOperationNotRecoverable
	}
	if e = c.store.UpdateOperation(ctx, id, models.OpStatusAbandoned, op.Stage, "ABANDONED", op.ErrorLog); e != nil {
		return nil, e
	}
	if e = c.store.PruneOperationBlobs(ctx, id); e != nil {
		return nil, e
	}
	return c.store.GetOperation(ctx, id)
}

// Restart restarts Traefik through the restricted capability. Only a running operation blocks it.
func (c *operations) Restart(ctx context.Context) error {
	if !c.mu.TryLock() {
		return ErrDeployInProgress
	}
	defer c.mu.Unlock()
	if blocked, e := c.recoveryBlocked(ctx); e != nil {
		return e
	} else if blocked {
		return ErrOperationRecoveryRequired
	}
	if _, e := c.runtime.RestartTraefikContainerAndConfirm(ctx); e != nil {
		return e
	}
	return c.store.SetSetting(ctx, settingRestartPending, "")
}

// CurrentDiskHashes returns the hashes of traefik.yml, the managed dynamic file and the console domains file.
func (c *operations) CurrentDiskHashes() (string, string, string, error) {
	d, e := c.readFiles()
	if e != nil {
		return "", "", "", e
	}
	sh, dh, domh := d.hashes()
	return sh, dh, domh, nil
}

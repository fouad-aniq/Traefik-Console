package engine

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"github.com/traefik-console/internal/docker"

	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
)

type BouncerVerifier interface {
	VerifyKey(context.Context, string) error
}

// bouncerURLVerifier verifies a key against the address the operation is saving, which may not be the
// committed one yet (first save, or a changed address).
type bouncerURLVerifier interface {
	VerifyKeyAt(context.Context, string, string) error
}

type machineURLVerifier interface {
	VerifyMachineAt(context.Context, string, string, string) error
}

type machineCredentialVerifier interface {
	VerifyMachine(context.Context, string, string) error
}

func (c *operations) SetBouncerVerifier(v BouncerVerifier) { c.bouncerVerifier = v }
func enrollmentID(key string) int64 {
	h := sha256.Sum256([]byte("bouncer:" + key))
	return int64(binary.BigEndian.Uint64(h[:8])&0x001ffffffffffffe) + 1
}

func machineEnrollmentID(key string) int64 {
	h := sha256.Sum256([]byte("machine:" + key))
	return int64(binary.BigEndian.Uint64(h[:8])&0x001ffffffffffffe) + 1
}

// integrationUpsert finds the CrowdSec integration row a mutation writes, if any.
func integrationUpsert(m *models.Mutation) int {
	for i := range m.Upserts {
		if m.Upserts[i].Kind == models.KindCrowdSec && m.Upserts[i].Name == "integration" {
			return i
		}
	}
	return -1
}

// enrollmentPlan decides, before any side effect, whether the operation registers a
// bouncer (auto_enroll) or must revoke old registrations (integration disabled).
func (c *operations) enrollmentPlan(ctx context.Context, state *db.State, m *models.Mutation, key string) error {
	idx := integrationUpsert(m)
	if idx < 0 {
		return nil
	}
	payload := m.Upserts[idx].Payload
	var cfg struct {
		Enabled             bool    `json:"enabled"`
		IntegrationMode     string  `json:"integration_mode"`
		AutoEnroll          bool    `json:"auto_enroll"`
		AutoEnrollMachine   bool    `json:"auto_enroll_machine"`
		KeepEnrollments     bool    `json:"keep_enrollments"`
		MachineEnrollmentID int64   `json:"machine_enrollment_id"`
		IDs                 []int64 `json:"enrollment_ids"`
	}
	if json.Unmarshal(payload, &cfg) != nil {
		return ErrValidationFailed
	}
	state.DesiredIntegration = append([]byte(nil), payload...)
	// A key may be registered for enabled protection, for an external bouncer, or while connection
	// settings of a disabled integration are saved (KeepEnrollments).
	if cfg.AutoEnroll && (cfg.Enabled || cfg.IntegrationMode == "external" || cfg.KeepEnrollments) {
		state.AttemptEnrollmentID = enrollmentID(key)
		if _, ok := c.runtime.(docker.BouncerRegistrar); !ok {
			return ErrRecreateRequired
		}
		if c.bouncerVerifier == nil {
			return ErrRecreateRequired
		}
	}
	if cfg.AutoEnrollMachine && (cfg.Enabled || cfg.IntegrationMode == "external" || cfg.KeepEnrollments) {
		state.AttemptMachineEnrollmentID = machineEnrollmentID(key)
		if _, ok := c.runtime.(docker.MachineRegistrar); !ok {
			return ErrRecreateRequired
		}
		if _, ok := c.bouncerVerifier.(machineCredentialVerifier); !ok {
			return ErrRecreateRequired
		}
	}
	if !cfg.Enabled && cfg.IntegrationMode != "external" && !cfg.KeepEnrollments {
		state.CleanupEnrollmentIDs = cfg.IDs
		state.CleanupMachineEnrollmentID = cfg.MachineEnrollmentID
	}
	return nil
}

// registerBouncer registers (or reuses) role-specific credentials and returns the
// FINAL integration payload before publication. Both roles use stable IDs derived
// from the request key, so replay never creates additional registrations.
func (c *operations) registerBouncer(ctx context.Context, state *db.State, candidate []models.SecretRow) (json.RawMessage, error) {
	if state.AttemptEnrollmentID == 0 && state.AttemptMachineEnrollmentID == 0 {
		return nil, nil
	}
	var cfg map[string]any
	if json.Unmarshal(state.DesiredIntegration, &cfg) != nil {
		return nil, ErrValidationFailed
	}
	secret := func(name string) (string, error) {
		for _, row := range candidate {
			if row.Name == name {
				return c.secrets.Decrypt(row.EncryptedValue)
			}
		}
		return c.secrets.DecryptSecret(ctx, name)
	}
	if state.AttemptEnrollmentID != 0 {
		key, err := secret("crowdsec_lapi_key")
		if err != nil {
			return nil, err
		}
		capability, ok := c.runtime.(docker.BouncerRegistrar)
		if !ok {
			return nil, ErrRecreateRequired
		}
		verify := func(key string) error {
			if v, ok := c.bouncerVerifier.(bouncerURLVerifier); ok {
				if lapiURL, _ := cfg["lapi_url"].(string); lapiURL != "" {
					return v.VerifyKeyAt(ctx, lapiURL, key)
				}
			}
			return c.bouncerVerifier.VerifyKey(ctx, key)
		}
		enrolled := false
		if err = verify(key); err != nil {
			if err = capability.RegisterBouncer(ctx, state.AttemptEnrollmentID, key); err != nil {
				return nil, err
			}
			enrolled = true
			if err = verify(key); err != nil {
				return nil, err
			}
		}
		var ids []int64
		if data, ok := cfg["enrollment_ids"]; ok {
			raw, _ := json.Marshal(data)
			_ = json.Unmarshal(raw, &ids)
		}
		if enrolled {
			state.CleanupEnrollmentIDs = append([]int64(nil), ids...)
			ids = []int64{state.AttemptEnrollmentID}
		} else {
			// Reusing a still-valid historical key creates no owned side effect;
			// rollback must not revoke that pre-existing registration.
			state.AttemptEnrollmentID = 0
		}
		cfg["enrollment_ids"] = ids
		cfg["auto_enroll"] = false
	}
	if state.AttemptMachineEnrollmentID != 0 {
		password, err := secret("crowdsec_machine_password")
		if err != nil {
			return nil, err
		}
		capability, ok := c.runtime.(docker.MachineRegistrar)
		if !ok {
			return nil, ErrRecreateRequired
		}
		verifier, ok := c.bouncerVerifier.(machineCredentialVerifier)
		if !ok {
			return nil, ErrRecreateRequired
		}
		machineID, err := capability.RegisterMachine(ctx, state.AttemptMachineEnrollmentID, password)
		if err != nil {
			return nil, err
		}
		if v, ok := c.bouncerVerifier.(machineURLVerifier); ok {
			if lapiURL, _ := cfg["lapi_url"].(string); lapiURL != "" {
				err = v.VerifyMachineAt(ctx, lapiURL, machineID, password)
			} else {
				err = verifier.VerifyMachine(ctx, machineID, password)
			}
		} else {
			err = verifier.VerifyMachine(ctx, machineID, password)
		}
		if err != nil {
			return nil, err
		}
		if previous, ok := cfg["machine_enrollment_id"].(float64); ok && int64(previous) > 0 {
			state.CleanupMachineEnrollmentID = int64(previous)
		}
		cfg["machine_id"] = machineID
		cfg["machine_enrollment_id"] = state.AttemptMachineEnrollmentID
		cfg["auto_enroll_machine"] = false
	}
	return json.Marshal(cfg)
}

// finishCleanup revokes the registrations a disabled integration left behind. The
// change itself is already committed; a failure leaves the operation in
// recovery_required (stage "cleanup") so it can be retried explicitly.
func (c *operations) finishCleanup(ctx context.Context, op *models.OperationRow) error {
	var state db.State
	if json.Unmarshal(op.PriorState, &state) != nil {
		return c.recoveryFailed(ctx, op, "cleanup")
	}
	if len(state.CleanupEnrollmentIDs) > 0 {
		capability, ok := c.runtime.(docker.BouncerRegistrar)
		if !ok {
			return c.recoveryFailed(ctx, op, "cleanup")
		}
		for _, id := range state.CleanupEnrollmentIDs {
			if e := capability.RevokeBouncer(ctx, id); e != nil {
				return c.recoveryFailed(ctx, op, "cleanup")
			}
		}
	}
	if state.CleanupMachineEnrollmentID > 0 {
		capability, ok := c.runtime.(docker.MachineRegistrar)
		if !ok {
			return c.recoveryFailed(ctx, op, "cleanup")
		}
		if e := capability.RevokeMachine(ctx, state.CleanupMachineEnrollmentID); e != nil {
			return c.recoveryFailed(ctx, op, "cleanup")
		}
	}
	status := models.OpStatusSucceeded
	if op.ReasonCode == reasonRestartRequired {
		status = models.OpStatusRestartRequired
	}
	if e := c.store.UpdateOperation(ctx, op.ID, status, "", op.ReasonCode, ""); e != nil {
		return e
	}
	if e := c.store.PruneOperationBlobs(ctx, op.ID); e != nil {
		return e
	}
	op.Status, op.Stage, op.ErrorLog = status, "", ""
	return nil
}

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
)

func TestCrowdSecEnrollmentIDsAreStableAndRoleSpecific(t *testing.T) {
	firstBouncer, secondBouncer := enrollmentID("request-key"), enrollmentID("request-key")
	firstMachine, secondMachine := machineEnrollmentID("request-key"), machineEnrollmentID("request-key")
	if firstBouncer <= 0 || firstMachine <= 0 || firstBouncer != secondBouncer || firstMachine != secondMachine || firstBouncer == firstMachine {
		t.Fatalf("enrollment identities are not stable and role-specific: %d %d", firstBouncer, firstMachine)
	}
}

func TestCrowdSecDisableRecordsBothOwnedCredentialCleanups(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"enabled": false, "enrollment_ids": []int64{11}, "machine_enrollment_id": 12})
	mutation := models.Mutation{Upserts: []models.ConfigItemRow{{Kind: models.KindCrowdSec, Name: "integration", Payload: payload}}}
	state := &db.State{}
	if err := (&operations{}).enrollmentPlan(context.Background(), state, &mutation, "request-key"); err != nil {
		t.Fatal(err)
	}
	if len(state.CleanupEnrollmentIDs) != 1 || state.CleanupEnrollmentIDs[0] != 11 || state.CleanupMachineEnrollmentID != 12 {
		t.Fatalf("owned cleanup was lost: %+v", state)
	}
}

func TestExternalCrowdSecDoesNotScheduleManagedCredentialCleanup(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"enabled": false, "integration_mode": "external", "enrollment_ids": []int64{11}, "machine_enrollment_id": 12})
	mutation := models.Mutation{Upserts: []models.ConfigItemRow{{Kind: models.KindCrowdSec, Name: "integration", Payload: payload}}}
	state := &db.State{}
	if err := (&operations{}).enrollmentPlan(context.Background(), state, &mutation, "request-key"); err != nil {
		t.Fatal(err)
	}
	if len(state.CleanupEnrollmentIDs) != 0 || state.CleanupMachineEnrollmentID != 0 {
		t.Fatalf("external mode scheduled managed cleanup: %+v", state)
	}
}

func TestCrowdSecAutoMachineRequiresFixedCapability(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"enabled": true, "auto_enroll_machine": true})
	mutation := models.Mutation{Upserts: []models.ConfigItemRow{{Kind: models.KindCrowdSec, Name: "integration", Payload: payload}}}
	state := &db.State{}
	err := (&operations{}).enrollmentPlan(context.Background(), state, &mutation, "request-key")
	if !errors.Is(err, ErrRecreateRequired) || state.AttemptMachineEnrollmentID != machineEnrollmentID("request-key") {
		t.Fatalf("automatic enrollment escaped its capability gate: %+v %v", state, err)
	}
}

func TestCrowdSecConnectionSaveKeepsEnrollmentsAndMayEnroll(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"enabled": false, "keep_enrollments": true, "auto_enroll": true, "enrollment_ids": []int64{11}})
	mutation := models.Mutation{Upserts: []models.ConfigItemRow{{Kind: models.KindCrowdSec, Name: "integration", Payload: payload}}}
	state := &db.State{}
	err := (&operations{}).enrollmentPlan(context.Background(), state, &mutation, "request-key")
	if len(state.CleanupEnrollmentIDs) != 0 {
		t.Fatalf("a connection save revoked the Console key: %+v", state)
	}
	if !errors.Is(err, ErrRecreateRequired) || state.AttemptEnrollmentID != enrollmentID("request-key") {
		t.Fatalf("key generation for a disabled integration escaped its capability gate: %+v %v", state, err)
	}
}

func TestCrowdSecMonitoringCredentialsCanBeCreatedWhileDisabled(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"enabled": false, "keep_enrollments": true, "auto_enroll_machine": true})
	mutation := models.Mutation{Upserts: []models.ConfigItemRow{{Kind: models.KindCrowdSec, Name: "integration", Payload: payload}}}
	state := &db.State{}
	err := (&operations{}).enrollmentPlan(context.Background(), state, &mutation, "request-key")
	if !errors.Is(err, ErrRecreateRequired) || state.AttemptMachineEnrollmentID != machineEnrollmentID("request-key") || state.CleanupMachineEnrollmentID != 0 {
		t.Fatalf("machine registration for a disabled integration: %+v %v", state, err)
	}
}

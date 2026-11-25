// Package docker exposes fixed container operations over an operator-configured
// Docker endpoint. Request handlers receive only the narrow capabilities below.
package docker

import (
	"context"
	"time"
)

type ContainerStatus struct {
	RestartAllowed bool              `json:"restart_allowed"`
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Image          string            `json:"image"`
	Running        bool              `json:"running"`
	Status         string            `json:"status"`
	RestartCount   int               `json:"restart_count"`
	StartedAt      time.Time         `json:"started_at"`
	SecretFileEnv  map[string]string `json:"secret_file_env,omitempty"`
}

type Decision struct {
	ID       int64  `json:"id"`
	Origin   string `json:"origin"`
	Type     string `json:"type"`
	Scope    string `json:"scope"`
	Value    string `json:"value"`
	Duration string `json:"duration"`
	Scenario string `json:"scenario"`
}
type TraefikContainerInspector interface {
	InspectTraefikContainer(context.Context) (*ContainerStatus, error)
}
type TraefikContainerRuntime interface {
	TraefikContainerInspector
	RestartTraefikContainerAndConfirm(context.Context) (*ContainerStatus, error)
	ProbeTraefikSecretFile(context.Context, string) error
}
type DecisionManager interface {
	ListDecisions(context.Context) ([]Decision, error)
	AddDecision(context.Context, string, string, string) error
	DeleteDecision(context.Context, string) error
	DeleteDecisionByID(context.Context, int64) error
}
type BouncerRegistrar interface {
	RegisterBouncer(context.Context, int64, string) error
	RevokeBouncer(context.Context, int64) error
}

type MachineRegistrar interface {
	RegisterMachine(context.Context, int64, string) (string, error)
	RevokeMachine(context.Context, int64) error
}

type AllowlistEntry struct {
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	Expiration  string `json:"expiration,omitempty"`
}

type Allowlist struct {
	Name           string           `json:"name"`
	ID             string           `json:"allowlist_id,omitempty"`
	Description    string           `json:"description"`
	Items          []AllowlistEntry `json:"items"`
	CreatedAt      string           `json:"created_at,omitempty"`
	UpdatedAt      string           `json:"updated_at,omitempty"`
	ConsoleManaged bool             `json:"console_managed"`
}

type AllowlistManager interface {
	ListAllowlists(context.Context) ([]Allowlist, error)
	CheckAllowlist(context.Context, string) (bool, string, error)
	CreateAllowlist(context.Context, string, string) error
	AddAllowlistEntry(context.Context, string, string, string, string) error
	RemoveAllowlistEntry(context.Context, string, string) error
	DeleteAllowlist(context.Context, string) error
}

// Operations is the composition root contract. Consumers receive the narrower capabilities above.
type Operations interface {
	TraefikContainerRuntime
	DecisionManager
	BouncerRegistrar
}

// Alert is one CrowdSec alert (a detected attack) as listed by cscli; only fields safe to show.
type Alert struct {
	ID          int64      `json:"id"`
	Scope       string     `json:"scope"`
	Value       string     `json:"value"`
	Reason      string     `json:"reason"`
	Country     string     `json:"country"`
	AS          string     `json:"as"`
	ASNumber    string     `json:"as_number,omitempty"`
	Kind        string     `json:"kind,omitempty"`
	Origin      string     `json:"origin,omitempty"`
	EventsCount int32      `json:"events_count"`
	CreatedAt   string     `json:"created_at"`
	Decisions   []Decision `json:"decisions,omitempty"`
}

// Bouncer is a client registered with the CrowdSec LAPI (the Traefik plugin is one).
type Bouncer struct {
	Name      string `json:"name"`
	IPAddress string `json:"ip_address"`
	Type      string `json:"type"`
	Version   string `json:"version"`
	LastPull  string `json:"last_pull"`
	Revoked   bool   `json:"revoked"`
}

// EngineState reports whether the registered CrowdSec container can be reached.
type EngineState struct {
	Configured bool   `json:"configured"`
	Running    bool   `json:"running"`
	Name       string `json:"name"`
	Image      string `json:"image"`
}

// CrowdSecInspector is the read-only view used by the status and statistics endpoints. It is separate from
// DecisionManager so existing implementations keep compiling; callers type-assert for it.
type CrowdSecInspector interface {
	CrowdSecEngine(context.Context) (*EngineState, error)
	ListAlerts(ctx context.Context, sinceHours int, limit int) ([]Alert, error)
	InspectAlert(context.Context, int64) (*Alert, error)
	ListBouncers(context.Context) ([]Bouncer, error)
}

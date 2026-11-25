package models

import (
	"encoding/json"
	"time"
)

// ItemKind defines the 7 entity types stored in config_items
type ItemKind string

const (
	KindResolver   ItemKind = "resolver"
	KindDomain     ItemKind = "domain"
	KindPlugin     ItemKind = "plugin"
	KindRouter     ItemKind = "router"
	KindService    ItemKind = "service"
	KindMiddleware ItemKind = "middleware"
	KindChain      ItemKind = "chain"
	KindTLS        ItemKind = "tls"
	KindCrowdSec   ItemKind = "crowdsec"
	KindEntryPoint ItemKind = "entrypoint"
)

// ItemScope determines if modifying this item requires restarting Traefik
type ItemScope string

const (
	ScopeStatic  ItemScope = "static"  // Resolvers, Plugins, EntryPoints -> Restart Required
	ScopeDynamic ItemScope = "dynamic" // Routers, Services, Middlewares -> Hot-Reload
)

// ItemStatus is the status a resource reports to the UI. Every stored row is
// committed, so the only value is "active"; the field is kept for one release so
// the frontend and backend can ship separately.
type ItemStatus string

const StatusActive ItemStatus = "active"

// ConfigItemRow maps 1:1 with the SQLite `config_items` table via sqlx.
// Version is the per-resource optimistic-concurrency counter (If-Match).
type ConfigItemRow struct {
	ID        string          `db:"id" json:"id"`
	Kind      ItemKind        `db:"kind" json:"kind"`
	Name      string          `db:"name" json:"name"`
	Scope     ItemScope       `db:"scope" json:"scope"`
	Payload   json.RawMessage `db:"payload" json:"payload,omitempty"`
	Version   int64           `db:"version" json:"version"`
	CreatedAt string          `db:"created_at" json:"created_at"`
	UpdatedAt string          `db:"updated_at" json:"updated_at"`
}

// UIResource is the unified object returned to the Svelte frontend for ANY list endpoint.
// It seamlessly combines items from SQLite ("managed") and items discovered from Traefik ("external").
type UIResource[T any] struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Origin       string     `json:"origin"`      // "managed" (SQLite) | "external" (Traefik file/docker)
	Provider     string     `json:"provider"`    // "file", "docker", "internal"
	Status       ItemStatus `json:"status"`      // always "active": every stored resource is committed
	ReadOnly     bool       `json:"read_only"`   // true if Origin == "external"
	LiveHealth   string     `json:"live_health"` // "ok", "warning", "error"
	LiveErrorMsg string     `json:"live_error_msg,omitempty"`
	Config       T          `json:"config"`            // The typed struct (e.g., RouterConfig, ResolverInput)
	Version      int64      `json:"version,omitempty"` // Send back as If-Match when updating or deleting
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
}

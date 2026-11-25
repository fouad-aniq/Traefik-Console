package middlewares

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/presentation"
	"github.com/traefik-console/internal/traefik/status"
	"github.com/traefik-console/internal/traefik/validation"
)

var allowedMiddlewareTypes = map[string]bool{
	"addPrefix": true, "basicAuth": true, "buffering": true, "circuitBreaker": true,
	"compress": true, "contentType": true, "digestAuth": true, "errors": true,
	"forwardAuth": true, "grpcWeb": true, "headers": true, "ipAllowList": true,
	"inFlightReq": true, "passTLSClientCert": true, "rateLimit": true,
	"redirectRegex": true, "redirectScheme": true, "replacePath": true,
	"replacePathRegex": true, "retry": true, "stripPrefix": true,
	"stripPrefixRegex": true, "plugin": true,
}

// TCP middlewares Traefik offers (ipWhiteList is its deprecated name for ipAllowList).
var allowedTCPMiddlewareTypes = map[string]bool{"inFlightConn": true, "ipAllowList": true}

// Protocols are the middleware protocols the console manages (UDP routers have no middlewares).
var Protocols = []string{models.ServiceHTTP, models.ServiceTCP}

type MiddlewareManager interface {
	// Standard & Plugin Middlewares
	ListMiddlewares(ctx context.Context) ([]models.UIResource[models.MiddlewareConfig], error)
	GetMiddleware(ctx context.Context, name string) (*models.UIResource[models.MiddlewareConfig], error)
	UpsertMiddleware(ctx context.Context, name string, input models.MiddlewareConfig, opts models.WriteOptions) (*models.WriteResult[models.MiddlewareConfig], error)
	DeleteMiddleware(ctx context.Context, name string, opts models.WriteOptions) (*models.WriteResult[models.MiddlewareConfig], error)

	// Middleware Chains
	ListChains(ctx context.Context) ([]models.UIResource[models.ChainConfig], error)
	GetChain(ctx context.Context, name string) (*models.UIResource[models.ChainConfig], error)
	UpsertChain(ctx context.Context, name string, input models.ChainConfig, opts models.WriteOptions) (*models.WriteResult[models.ChainConfig], error)
	DeleteChain(ctx context.Context, name string, opts models.WriteOptions) (*models.WriteResult[models.ChainConfig], error)
}

var resolverNameRegex = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

func (m *middlewareManager) ListMiddlewares(ctx context.Context) ([]models.UIResource[models.MiddlewareConfig], error) {
	dbRows, err := m.store.ListItemsByKind(ctx, models.KindMiddleware)
	if err != nil {
		return nil, err
	}

	observed, err := m.inspector.ObserveRuntime(ctx)
	if err != nil {
		return nil, err
	}
	snapshots := map[string]*status.RuntimeResources{models.ServiceHTTP: &observed.HTTP, models.ServiceTCP: &observed.TCP}

	seen := make(map[string]bool)
	result := make([]models.UIResource[models.MiddlewareConfig], 0)

	for _, row := range dbRows {
		seen[row.Name] = true
		seen[row.Name+"@file"] = true

		cfg, err := presentation.DecodeUIConfig[models.MiddlewareConfig](row)
		if err != nil {
			return nil, err
		}
		cfg.Protocol = models.MiddlewareProtocol(cfg)

		health := "unknown"
		var errMsg string
		if extSnap := snapshots[cfg.Protocol]; extSnap != nil {
			if liveItem, ok := extSnap.Middlewares[row.Name+"@file"]; ok {
				if liveItem.Status == "enabled" {
					health = "loaded"
				}
				if liveItem.Status == "error" || liveItem.ErrorMsg != "" {
					health = "error"
					errMsg = liveItem.ErrorMsg
				}
			}
		}

		result = append(result, models.UIResource[models.MiddlewareConfig]{
			ID:           row.ID,
			UpdatedAt:    presentation.ParseTimestamp(row.UpdatedAt),
			Name:         row.Name,
			Origin:       "managed",
			Provider:     "file",
			Status:       models.StatusActive,
			ReadOnly:     false,
			LiveHealth:   health,
			LiveErrorMsg: errMsg,
			Config:       cfg,
			Version:      row.Version,
		})
	}

	// Append External Middlewares (@docker, @internal, or unmanaged @file), HTTP then TCP.
	for _, protocol := range Protocols {
		extSnap := snapshots[protocol]
		if extSnap == nil {
			continue
		}
		prefix := "external:middleware:"
		if protocol == models.ServiceTCP {
			prefix = "external:tcp-middleware:"
		}
		for fullKey, extItem := range extSnap.Middlewares {
			if seen[fullKey] || (extItem.Provider == "file" && seen[extItem.Name]) {
				continue
			}
			// Skip chains here; chains are returned in ListChains
			if _, isChain := extItem.Raw["chain"]; isChain {
				continue
			}
			result = append(result, models.UIResource[models.MiddlewareConfig]{
				ID:           prefix + fullKey,
				Name:         fullKey,
				Origin:       "external",
				Provider:     extItem.Provider,
				Status:       models.StatusActive,
				ReadOnly:     true,
				LiveHealth:   extItem.Status,
				LiveErrorMsg: extItem.ErrorMsg,
				Config:       models.MiddlewareConfig{Protocol: protocol, Type: "external", Spec: extItem.Raw},
			})
		}
	}

	return result, nil
}

func (m *middlewareManager) GetMiddleware(ctx context.Context, name string) (*models.UIResource[models.MiddlewareConfig], error) {
	all, err := m.ListMiddlewares(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range all {
		if item.Name == name || strings.TrimSuffix(item.Name, "@file") == name {
			return &item, nil
		}
	}
	return nil, files.ErrSourceFileNotFound
}

func (m *middlewareManager) UpsertMiddleware(ctx context.Context, name string, input models.MiddlewareConfig, opts models.WriteOptions) (*models.WriteResult[models.MiddlewareConfig], error) {
	name = strings.TrimSpace(name)
	if !resolverNameRegex.MatchString(name) {
		return nil, ErrMiddlewareInvalidName
	}
	protocol := models.MiddlewareProtocol(input)
	if protocol != models.ServiceHTTP && protocol != models.ServiceTCP {
		return nil, ErrMiddlewareInvalidProtocol
	}
	// A middleware keeps its protocol: routers of the other protocol could not use it any more.
	if previous, e := m.store.GetItem(ctx, models.KindMiddleware, name); e != nil {
		return nil, e
	} else if previous != nil {
		var was models.MiddlewareConfig
		if json.Unmarshal(previous.Payload, &was) == nil && models.MiddlewareProtocol(was) != protocol {
			return nil, fmt.Errorf("%w: '%s' is a %s middleware", ErrMiddlewareInvalidProtocol, name, strings.ToUpper(models.MiddlewareProtocol(was)))
		}
	}
	if protocol == models.ServiceTCP {
		return m.upsertTCPMiddleware(ctx, name, input, opts)
	}
	input.Protocol = "" // HTTP payloads are stored as before
	if !allowedMiddlewareTypes[input.Type] {
		return nil, fmt.Errorf("%w: '%s'", ErrMiddlewareInvalidType, input.Type)
	}
	if input.Type == "plugin" && strings.TrimSpace(input.PluginName) == "" {
		return nil, ErrMiddlewareInvalidType
	}
	input.UsedBy = nil // computed at read time; never stored
	if input.Spec == nil {
		input.Spec = make(map[string]any)
	}

	mutation := models.Mutation{}
	// Users entered as name + password are hashed here and continue as htpasswd lines below.
	if raw, ok := input.Spec[credentialsKey]; ok {
		if input.Type != "basicAuth" {
			return nil, fmt.Errorf("%w: users can be entered this way for basicAuth only", ErrInvalidCredentials)
		}
		if _, both := input.Spec["users_htpasswd"]; both {
			return nil, fmt.Errorf("%w: send users or htpasswd lines, not both", ErrInvalidCredentials)
		}
		users, err := parseCredentials(raw)
		if err != nil {
			return nil, err
		}
		lines, err := htpasswdLines(users)
		if err != nil {
			return nil, err
		}
		delete(input.Spec, credentialsKey)
		input.Spec["users_htpasswd"] = lines
	}
	// If basicAuth includes raw "users_htpasswd", store it in a 0600 secret file and set usersFile!
	if input.Type == "basicAuth" {
		if rawUsers, ok := input.Spec["users_htpasswd"].(string); ok && strings.TrimSpace(rawUsers) != "" {
			if !strings.HasPrefix(rawUsers, "••••••••") && rawUsers != "[redacted]" {
				secName := "basicauth_" + name
				secRow, err := m.secrets.PrepareSecret(ctx, secName, "basic_auth", "", rawUsers)
				if err != nil {
					return nil, err
				}
				input.Spec["usersFile"] = secRow.FilePath
				mutation.Secrets = append(mutation.Secrets, *secRow)
				delete(input.Spec, "users")
				delete(input.Spec, "users_htpasswd")
			}
		}
	}

	if input.Type == "basicAuth" {
		if raw, ok := input.Spec["users_htpasswd"].(string); ok && (raw == "[redacted]" || strings.HasPrefix(raw, "••••••••")) {
			previous, e := m.store.GetItem(ctx, models.KindMiddleware, name)
			if e != nil || previous == nil {
				return nil, ErrMiddlewareInvalidType
			}
			var cfg models.MiddlewareConfig
			if json.Unmarshal(previous.Payload, &cfg) != nil || cfg.Spec["usersFile"] == nil {
				return nil, ErrMiddlewareInvalidType
			}
			input.Spec["usersFile"] = cfg.Spec["usersFile"]
			delete(input.Spec, "users_htpasswd")
		}
	}
	if err := validation.ValidateNativeMiddleware(input.Type, input.Spec); err != nil {
		return nil, fmt.Errorf("%w: invalid native middleware fields", ErrMiddlewareInvalidType)
	}
	rawJSON, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}

	mutation.Upserts = []models.ConfigItemRow{{ID: "middleware:" + name, Kind: models.KindMiddleware, Name: name, Scope: models.ScopeDynamic, Payload: rawJSON}}
	return m.writeMiddleware(ctx, name, mutation, opts)
}

// upsertTCPMiddleware stores a TCP middleware: one of the types Traefik offers for TCP, no secrets.
func (m *middlewareManager) upsertTCPMiddleware(ctx context.Context, name string, input models.MiddlewareConfig, opts models.WriteOptions) (*models.WriteResult[models.MiddlewareConfig], error) {
	if !allowedTCPMiddlewareTypes[input.Type] {
		return nil, fmt.Errorf("%w: '%s' is not a TCP middleware", ErrMiddlewareInvalidType, input.Type)
	}
	if input.PluginName != "" || len(input.SecretRefs) > 0 {
		return nil, fmt.Errorf("%w: a TCP middleware has no plugin or secrets", ErrMiddlewareInvalidType)
	}
	input.Protocol = models.ServiceTCP
	input.UsedBy = nil
	if input.Spec == nil {
		input.Spec = map[string]any{}
	}
	if err := validation.ValidateNativeTCPMiddleware(input.Type, input.Spec); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMiddlewareInvalidType, err)
	}
	rawJSON, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	mutation := models.Mutation{Upserts: []models.ConfigItemRow{{ID: "middleware:" + name, Kind: models.KindMiddleware, Name: name, Scope: models.ScopeDynamic, Payload: rawJSON}}}
	return m.writeMiddleware(ctx, name, mutation, opts)
}

func (m *middlewareManager) writeMiddleware(ctx context.Context, name string, mutation models.Mutation, opts models.WriteOptions) (*models.WriteResult[models.MiddlewareConfig], error) {
	return engine.Write(ctx, m.applier, mutation, opts, func(ctx context.Context) (*models.UIResource[models.MiddlewareConfig], error) {
		r, e := m.committedRow(ctx, models.KindMiddleware, name)
		if e != nil {
			return nil, e
		}
		res, e := presentation.ManagedResource[models.MiddlewareConfig](*r, "file")
		if e == nil && res != nil {
			res.Config.Protocol = models.MiddlewareProtocol(res.Config)
		}
		return res, e
	})
}

func (m *middlewareManager) DeleteMiddleware(ctx context.Context, name string, opts models.WriteOptions) (*models.WriteResult[models.MiddlewareConfig], error) {
	if err := m.requireManaged(ctx, models.KindMiddleware, name); err != nil {
		return nil, err
	}
	protocol := models.ServiceHTTP
	if row, e := m.store.GetItem(ctx, models.KindMiddleware, name); e == nil && row != nil {
		var cfg models.MiddlewareConfig
		if json.Unmarshal(row.Payload, &cfg) == nil {
			protocol = models.MiddlewareProtocol(cfg)
		}
	}
	if err := m.checkMiddlewareNotReferencedBy(ctx, name, protocol); err != nil {
		return nil, err
	}
	return engine.Write[models.MiddlewareConfig](ctx, m.applier, models.Mutation{Deletes: []models.ItemRef{{Kind: models.KindMiddleware, Name: name}}}, opts, nil)
}

// ============================================================================
// MIDDLEWARE CHAINS
// ============================================================================

func (m *middlewareManager) ListChains(ctx context.Context) ([]models.UIResource[models.ChainConfig], error) {
	dbRows, err := m.store.ListItemsByKind(ctx, models.KindChain)
	if err != nil {
		return nil, err
	}

	result := make([]models.UIResource[models.ChainConfig], 0)
	for _, row := range dbRows {
		cfg, err := presentation.DecodeUIConfig[models.ChainConfig](row)
		if err != nil {
			return nil, err
		}

		health := "unknown"

		result = append(result, models.UIResource[models.ChainConfig]{
			ID:         row.ID,
			UpdatedAt:  presentation.ParseTimestamp(row.UpdatedAt),
			Name:       row.Name,
			Origin:     "managed",
			Provider:   "file",
			Status:     models.StatusActive,
			ReadOnly:   false,
			LiveHealth: health,
			Config:     cfg,
			Version:    row.Version,
		})
	}
	observed, err := m.inspector.ObserveRuntime(ctx)
	if err != nil {
		return nil, err
	}
	snapshot := &observed.HTTP
	seen := map[string]bool{}
	for _, v := range result {
		seen[v.Name+"@file"] = true
	}
	for key, item := range snapshot.Middlewares {
		chain, ok := item.Raw["chain"].(map[string]any)
		if !ok || seen[key] {
			continue
		}
		bytes, e := json.Marshal(chain)
		if e != nil {
			return nil, e
		}
		var cfg models.ChainConfig
		if json.Unmarshal(bytes, &cfg) != nil {
			return nil, ErrMiddlewareInvalidType
		}
		result = append(result, models.UIResource[models.ChainConfig]{ID: "external:chain:" + key, Name: key, Origin: "external", Provider: item.Provider, Status: models.StatusActive, ReadOnly: true, LiveHealth: item.Status, Config: cfg})
	}
	return result, nil
}

func (m *middlewareManager) UpsertChain(ctx context.Context, name string, input models.ChainConfig, opts models.WriteOptions) (*models.WriteResult[models.ChainConfig], error) {
	name = strings.TrimSpace(name)
	if !resolverNameRegex.MatchString(name) {
		return nil, ErrMiddlewareInvalidName
	}

	input.UsedBy = nil // computed at read time; never stored
	if len(input.Middlewares) == 0 {
		return nil, ErrChainEmpty
	}

	for _, child := range input.Middlewares {
		clean := strings.TrimSpace(child)
		if strings.HasSuffix(clean, "@tcp") || strings.HasSuffix(clean, "@udp") {
			return nil, ErrMiddlewareInvalidType
		}
		if strings.TrimSuffix(clean, "@file") == name {
			return nil, ErrChainSelfReference
		}
		// Traefik has no TCP chains: a chain only groups HTTP middlewares.
		if !strings.Contains(strings.TrimSuffix(clean, "@file"), "@") {
			if row, e := m.store.GetItem(ctx, models.KindMiddleware, strings.TrimSuffix(clean, "@file")); e != nil {
				return nil, e
			} else if row != nil {
				var member models.MiddlewareConfig
				if json.Unmarshal(row.Payload, &member) == nil && models.MiddlewareProtocol(member) != models.ServiceHTTP {
					return nil, fmt.Errorf("%w: '%s' is a TCP middleware; a chain only holds HTTP middlewares", ErrMiddlewareInvalidType, clean)
				}
			}
		}
	}

	rawJSON, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}

	row := models.ConfigItemRow{ID: "chain:" + name, Kind: models.KindChain, Name: name, Scope: models.ScopeDynamic, Payload: rawJSON}
	return engine.Write(ctx, m.applier, models.Mutation{Upserts: []models.ConfigItemRow{row}}, opts, func(ctx context.Context) (*models.UIResource[models.ChainConfig], error) {
		r, e := m.committedRow(ctx, models.KindChain, name)
		if e != nil {
			return nil, e
		}
		return presentation.ManagedResource[models.ChainConfig](*r, "file")
	})
}

func (m *middlewareManager) GetChain(ctx context.Context, name string) (*models.UIResource[models.ChainConfig], error) {
	chains, err := m.ListChains(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range chains {
		if c.Name == name || strings.TrimSuffix(c.Name, "@file") == name {
			return &c, nil
		}
	}
	return nil, files.ErrSourceFileNotFound
}

func (m *middlewareManager) DeleteChain(ctx context.Context, name string, opts models.WriteOptions) (*models.WriteResult[models.ChainConfig], error) {
	if err := m.requireManaged(ctx, models.KindChain, name); err != nil {
		return nil, err
	}
	if err := m.checkMiddlewareNotReferenced(ctx, name); err != nil {
		return nil, err
	}
	return engine.Write[models.ChainConfig](ctx, m.applier, models.Mutation{Deletes: []models.ItemRef{{Kind: models.KindChain, Name: name}}}, opts, nil)
}

func (m *middlewareManager) checkMiddlewareNotReferenced(ctx context.Context, targetName string) error {
	return m.checkMiddlewareNotReferencedBy(ctx, targetName, models.ServiceHTTP)
}

// checkMiddlewareNotReferencedBy refuses a delete while a router of the same protocol, or (for HTTP)
// a chain, still uses the name: HTTP and TCP middlewares are separate pools in Traefik.
func (m *middlewareManager) checkMiddlewareNotReferencedBy(ctx context.Context, targetName, protocol string) error {
	// 1. Check Routers
	routers, err := m.store.ListItemsByKind(ctx, models.KindRouter)
	if err != nil {
		return err
	}
	for _, rRow := range routers {
		var rCfg models.RouterConfig
		if json.Unmarshal(rRow.Payload, &rCfg) == nil && models.RouterProtocol(rCfg) == protocol {
			for _, mw := range rCfg.Middlewares {
				if strings.TrimSuffix(mw, "@file") == targetName {
					return fmt.Errorf("%w: used by router '%s'", ErrMiddlewareInUse, rRow.Name)
				}
			}
		}
	}

	// 2. Check Chains (HTTP only)
	if protocol != models.ServiceHTTP {
		return nil
	}
	chains, err := m.store.ListItemsByKind(ctx, models.KindChain)
	if err != nil {
		return err
	}
	for _, cRow := range chains {
		if cRow.Name == targetName {
			continue
		}
		var cCfg models.ChainConfig
		if json.Unmarshal(cRow.Payload, &cCfg) == nil {
			for _, mw := range cCfg.Middlewares {
				if strings.TrimSuffix(mw, "@file") == targetName {
					return fmt.Errorf("%w: used by middleware chain '%s'", ErrMiddlewareInUse, cRow.Name)
				}
			}
		}
	}

	// 3. Check entrypoints: their default middlewares always name the provider (`name@file`).
	entryPoints, err := m.store.ListItemsByKind(ctx, models.KindEntryPoint)
	if err != nil {
		return err
	}
	for _, eRow := range entryPoints {
		var eCfg models.EntryPointConfig
		if json.Unmarshal(eRow.Payload, &eCfg) == nil && eCfg.HTTP != nil {
			for _, mw := range eCfg.HTTP.Middlewares {
				if mw == targetName+"@file" {
					return fmt.Errorf("%w: used by entrypoint '%s'", ErrMiddlewareInUse, eRow.Name)
				}
			}
		}
	}
	return nil
}

// requireManaged refuses a delete of something the console does not manage: a name Traefik knows
// from another source is read-only, any other unknown name is not found. Without this, deleting
// `auth@docker` or a typo "succeeded" without removing anything.
func (m *middlewareManager) requireManaged(ctx context.Context, kind models.ItemKind, name string) error {
	row, err := m.store.GetItem(ctx, kind, name)
	if err != nil {
		return err
	}
	if row != nil {
		return nil
	}
	if strings.Contains(name, "@") {
		return files.ErrCannotModifyReadOnly
	}
	// Best effort: an unmanaged file-provider middleware of that name is read-only, not missing.
	if observed, e := m.inspector.ObserveRuntime(ctx); e == nil {
		for _, snapshot := range []status.RuntimeResources{observed.HTTP, observed.TCP} {
			if _, ok := snapshot.Middlewares[name+"@file"]; ok {
				return files.ErrCannotModifyReadOnly
			}
		}
	}
	return files.ErrSourceFileNotFound
}

// committedRow reads a saved row from SQLite only; a successful write never depends on Traefik's API.
func (m *middlewareManager) committedRow(ctx context.Context, kind models.ItemKind, name string) (*models.ConfigItemRow, error) {
	row, err := m.store.GetItem(ctx, kind, name)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, files.ErrSourceFileNotFound
	}
	return row, nil
}

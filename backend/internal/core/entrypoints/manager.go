// Package entrypoints manages the listeners of traefik.yml (`entryPoints.<name>`).
//
// An entrypoint created here is owned by the console. One that already exists in traefik.yml is
// read-only until the operator takes it over ("adopts" it); its original definition is kept so
// releasing it puts the file back as it was.
package entrypoints

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/presentation"
	"github.com/traefik-console/internal/traefik/status"
)

type EntryPointManager interface {
	ListEntryPoints(ctx context.Context) ([]models.UIResource[models.EntryPointConfig], error)
	GetEntryPoint(ctx context.Context, name string) (*models.UIResource[models.EntryPointConfig], error)
	UpsertEntryPoint(ctx context.Context, input models.EntryPointConfig, opts models.WriteOptions) (*models.WriteResult[models.EntryPointConfig], error)
	// DeleteEntryPoint removes a console-created entrypoint, or releases an adopted one back to
	// its original traefik.yml definition.
	DeleteEntryPoint(ctx context.Context, name string, opts models.WriteOptions) (*models.WriteResult[models.EntryPointConfig], error)
}

type staticConfigReader interface{ ReadStaticConfig() ([]byte, error) }

type entryPointManager struct {
	store   db.ItemReader
	files   staticConfigReader
	applier engine.Applier
}

func NewEntryPointManager(store db.ItemReader, filesMgr staticConfigReader, applier engine.Applier) EntryPointManager {
	return &entryPointManager{store: store, files: filesMgr, applier: applier}
}

// world is everything one request needs to know about the entrypoints and what points at them.
type world struct {
	managed  map[string]models.EntryPointConfig
	rows     map[string]models.ConfigItemRow
	declared []declared // traefik.yml, file order
	static   *status.StaticSnapshot
	routers  []models.ConfigItemRow
	// resolverEntryPoints maps each resolver using the HTTP-01 challenge to its entrypoint.
	resolverEntryPoints map[string]string
	refs                references
}

func (m *entryPointManager) load(ctx context.Context) (*world, error) {
	rawStatic, err := m.files.ReadStaticConfig()
	if err != nil {
		return nil, err
	}
	w := &world{managed: map[string]models.EntryPointConfig{}, rows: map[string]models.ConfigItemRow{}, resolverEntryPoints: map[string]string{}}
	if w.declared, err = readDeclared(rawStatic); err != nil {
		return nil, err
	}
	if w.static, err = status.DiscoverStatic(rawStatic); err != nil {
		return nil, err
	}
	rows, err := m.store.ListItemsByKind(ctx, models.KindEntryPoint)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		cfg, err := presentation.DecodeUIConfig[models.EntryPointConfig](row)
		if err != nil {
			return nil, err
		}
		cfg.Name = row.Name
		w.managed[row.Name], w.rows[row.Name] = cfg, row
	}
	if w.routers, err = m.store.ListItemsByKind(ctx, models.KindRouter); err != nil {
		return nil, err
	}

	w.refs = references{Peers: map[string]peer{}, Resolvers: map[string]bool{}, Middlewares: map[string]bool{}}
	for _, d := range w.declared {
		if _, ok := w.managed[d.Name]; ok {
			continue
		}
		cfg, _ := fromTraefik(d.Name, d.Raw)
		w.refs.Peers[d.Name] = peer{Protocol: cfg.Protocol, Host: cfg.Host, Port: cfg.Port, HTTP3: cfg.HTTP3 != nil}
	}
	for name, cfg := range w.managed {
		w.refs.Peers[name] = peer{Protocol: cfg.Protocol, Host: cfg.Host, Port: cfg.Port, HTTP3: cfg.HTTP3 != nil}
	}
	for name, item := range w.static.Resolvers {
		w.refs.Resolvers[name] = true
		if challenge, ok := item.Raw["httpChallenge"].(map[string]any); ok {
			if ep, _ := challenge["entryPoint"].(string); ep != "" {
				w.resolverEntryPoints[name] = ep
			}
		}
	}
	resolvers, err := m.store.ListItemsByKind(ctx, models.KindResolver)
	if err != nil {
		return nil, err
	}
	for _, row := range resolvers {
		w.refs.Resolvers[row.Name] = true
		var cfg models.ResolverInput
		if json.Unmarshal(row.Payload, &cfg) == nil && cfg.Challenge == "http01" && cfg.EntryPoint != "" {
			w.resolverEntryPoints[row.Name] = cfg.EntryPoint
		} else {
			delete(w.resolverEntryPoints, row.Name)
		}
	}
	for _, kind := range []models.ItemKind{models.KindMiddleware, models.KindChain} {
		items, err := m.store.ListItemsByKind(ctx, kind)
		if err != nil {
			return nil, err
		}
		for _, row := range items {
			var cfg models.MiddlewareConfig
			if kind == models.KindChain || (json.Unmarshal(row.Payload, &cfg) == nil && models.MiddlewareProtocol(cfg) == models.ServiceHTTP) {
				w.refs.Middlewares[row.Name] = true
			}
		}
	}
	return w, nil
}

// external returns the traefik.yml definition of an entrypoint the console does not manage.
func (w *world) external(name string) (declared, bool) {
	if _, ok := w.managed[name]; ok {
		return declared{}, false
	}
	for _, d := range w.declared {
		if d.Name == name {
			return d, true
		}
	}
	return declared{}, false
}

// usage names what references an entrypoint. Empty usage means nothing breaks when it goes away.
func (w *world) usage(name string) *models.EntryPointUsage {
	u := &models.EntryPointUsage{}
	for _, row := range w.routers {
		var cfg models.RouterConfig
		if json.Unmarshal(row.Payload, &cfg) == nil && slices.Contains(cfg.EntryPoints, name) {
			u.Routers = append(u.Routers, row.Name)
		}
	}
	for resolver, ep := range w.resolverEntryPoints {
		if ep == name {
			u.Resolvers = append(u.Resolvers, resolver)
		}
	}
	redirects := func(other string, cfg models.EntryPointConfig) {
		if other != name && cfg.HTTP != nil && cfg.HTTP.Redirect != nil && cfg.HTTP.Redirect.To == name {
			u.EntryPoints = append(u.EntryPoints, other)
		}
	}
	for other, cfg := range w.managed {
		redirects(other, cfg)
	}
	for _, d := range w.declared {
		if _, ok := w.managed[d.Name]; !ok {
			cfg, _ := fromTraefik(d.Name, d.Raw)
			redirects(d.Name, cfg)
		}
	}
	sort.Strings(u.Routers)
	sort.Strings(u.Resolvers)
	sort.Strings(u.EntryPoints)
	return u
}

func used(u *models.EntryPointUsage) bool {
	return len(u.Routers)+len(u.Resolvers)+len(u.EntryPoints) > 0
}

// ListEntryPoints returns the console-managed entrypoints and, read-only, the ones only declared in traefik.yml.
func (m *entryPointManager) ListEntryPoints(ctx context.Context) ([]models.UIResource[models.EntryPointConfig], error) {
	w, err := m.load(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]models.UIResource[models.EntryPointConfig], 0, len(w.managed)+len(w.declared))
	for name, cfg := range w.managed {
		row := w.rows[name]
		cfg.Usage = w.usage(name)
		cfg.Original = nil
		result = append(result, models.UIResource[models.EntryPointConfig]{
			ID: row.ID, UpdatedAt: presentation.ParseTimestamp(row.UpdatedAt), Name: name, Origin: "managed", Provider: "static",
			Status: models.StatusActive, LiveHealth: "unknown", Config: cfg, Version: row.Version,
		})
	}
	for _, d := range w.declared {
		if _, ok := w.managed[d.Name]; ok {
			continue
		}
		cfg, unsupported := fromTraefik(d.Name, d.Raw)
		cfg.AdoptBlocker = adoptBlocker(d.Name, unsupported)
		cfg.Adoptable = cfg.AdoptBlocker == ""
		cfg.Usage = w.usage(d.Name)
		cfg.RenderedNode = d.Raw
		result = append(result, models.UIResource[models.EntryPointConfig]{
			ID: "external:entrypoint:" + d.Name, Name: d.Name, Origin: "external", Provider: "static",
			Status: models.StatusActive, ReadOnly: true, LiveHealth: "unknown", Config: cfg,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (m *entryPointManager) GetEntryPoint(ctx context.Context, name string) (*models.UIResource[models.EntryPointConfig], error) {
	all, err := m.ListEntryPoints(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range all {
		if item.Name == name {
			return &item, nil
		}
	}
	return nil, files.ErrSourceFileNotFound
}

// UpsertEntryPoint validates the entrypoint against the others and applies it as one operation.
// traefik.yml changes, so Traefik restarts (or a manual restart is reported).
func (m *entryPointManager) UpsertEntryPoint(ctx context.Context, in models.EntryPointConfig, opts models.WriteOptions) (*models.WriteResult[models.EntryPointConfig], error) {
	in.Name = strings.TrimSpace(in.Name)
	if !nameRegex.MatchString(in.Name) {
		return nil, ErrEntryPointInvalidName
	}
	w, err := m.load(ctx)
	if err != nil {
		return nil, err
	}

	wantsAdoption := in.Adopted
	in.Adopted, in.Original = false, nil
	previous, exists := w.managed[in.Name]
	portChanged := true
	switch ext, isExternal := w.external(in.Name); {
	case exists:
		// Ownership is decided when the entrypoint is first saved and never by a later request.
		in.Adopted, in.Original = previous.Adopted, previous.Original
		if in.Protocol != "" && !strings.EqualFold(in.Protocol, previous.Protocol) {
			return nil, ErrEntryPointProtocol
		}
		in.Protocol = previous.Protocol
		portChanged = previous.Port != in.Port || (previous.HTTP3 == nil) != (in.HTTP3 == nil)
	case isExternal:
		if !wantsAdoption {
			return nil, files.ErrCannotModifyReadOnly
		}
		current, unsupported := fromTraefik(ext.Name, ext.Raw)
		if adoptBlocker(ext.Name, unsupported) != "" {
			return nil, ErrEntryPointNotAdoptable
		}
		if in.Protocol != "" && !strings.EqualFold(in.Protocol, current.Protocol) {
			return nil, ErrEntryPointProtocol
		}
		in.Protocol, in.Adopted, in.Original = current.Protocol, true, ext.Raw
		if in.Original == nil {
			in.Original = map[string]any{}
		}
		portChanged = current.Port != in.Port || (current.HTTP3 == nil) != (in.HTTP3 == nil)
	}

	if err := build(&in, w.refs); err != nil {
		return nil, err
	}
	if portChanged {
		in.Warnings = append(in.Warnings, fmt.Sprintf("Traefik must be reachable on port %d/%s: publish it on the Traefik container (or open it on the host). The console cannot change container ports.", in.Port, in.Protocol))
	}

	payload, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	row := models.ConfigItemRow{ID: "entrypoint:" + in.Name, Kind: models.KindEntryPoint, Name: in.Name, Scope: models.ScopeStatic, Payload: payload}
	return engine.Write(ctx, m.applier, models.Mutation{Upserts: []models.ConfigItemRow{row}}, opts, func(ctx context.Context) (*models.UIResource[models.EntryPointConfig], error) {
		return m.GetEntryPoint(ctx, in.Name)
	})
}

func (m *entryPointManager) DeleteEntryPoint(ctx context.Context, name string, opts models.WriteOptions) (*models.WriteResult[models.EntryPointConfig], error) {
	w, err := m.load(ctx)
	if err != nil {
		return nil, err
	}
	cfg, exists := w.managed[name]
	if !exists {
		if _, isExternal := w.external(name); isExternal {
			return nil, files.ErrCannotModifyReadOnly
		}
		return nil, files.ErrSourceFileNotFound
	}
	// Releasing an adopted entrypoint keeps the listener (its original definition returns), so
	// nothing that references it breaks. Removing a console-created one must not orphan anything.
	if !cfg.Adopted {
		if u := w.usage(name); used(u) {
			var by []string
			for label, names := range map[string][]string{"router": u.Routers, "resolver": u.Resolvers, "entrypoint": u.EntryPoints} {
				for _, n := range names {
					by = append(by, label+" '"+n+"'")
				}
			}
			sort.Strings(by)
			return nil, fmt.Errorf("%w: used by %s", ErrEntryPointInUse, strings.Join(by, ", "))
		}
		if len(w.refs.Peers) <= 1 {
			return nil, ErrEntryPointLast
		}
	}
	return engine.Write[models.EntryPointConfig](ctx, m.applier, models.Mutation{Deletes: []models.ItemRef{{Kind: models.KindEntryPoint, Name: name}}}, opts, nil)
}

# core-v2 API analysis for frontend-v2

Sources: `core-v2/internal/api/handlers/router.go` (51 routes), `api/openapi.json`
(OpenAPI 3.1, v0.3.0), `docs/architecture/core-v2-frontend-integration.md`.
Base: `http://127.0.0.1:8081`, JSON only, **no authentication** (deferred).

## 1. What changes versus the old frontend (app/frontend → app/core)

| Topic | Old (app/core) | New (core-v2) |
|---|---|---|
| Auth | login/setup/me, HttpOnly cookie, `X-CSRF-Token`, 401 handler | **None.** Drop `features/auth`, `lib/stores/auth`, CSRF headers, login/setup views, logout in shell |
| Edit model | per-kind preview → apply (`/tls/previews`, `/drafts/*`, jobs) | **Direct writes**: `PUT/POST/DELETE` apply immediately as one operation; `?dry_run=true` previews; `If-Match "<version>"` / `If-None-Match: *`; `Idempotency-Key` |
| Item shape | `TLSItem {name, ownership, data}` | `*Resource {id,name,version,config,status,origin,provider,read_only,live_health,live_error_msg,updated_at}` |
| Ownership | `ownership: managed/external` | `origin: managed/external` + `read_only` |
| Pending state | `status` per job | none: there are no drafts. `status` is `active` (or health for runtime-backed items) |
| History | backups + revisions + jobs | user snapshots (`/snapshots`, keyset paging) + operations log (`/operations`) |
| Idempotency | job ids | `idempotency_key` (UUID) in apply/rollback body |
| Naming | camelCase models | **snake_case** (except `Overview` and `ServiceDiagnostic`, camelCase) |
| Lists | arrays | arrays **or null** → always `items ?? []` |

## 2. Write protocol (the core of the client)

1. Read the resource; keep its integer `version`.
2. `PUT|DELETE /api/v1/<kind>/{name}` with `If-Match: "<version>"` to change or delete, or `If-None-Match: *` to create. Body = full `*Input` (no PATCH, no nulls, no unknown keys). Neither header: 428; malformed or both: 400.
3. Add `?dry_run=true` to preview: `{preview: {changes[] (created|updated|deleted|impacted), static_diff, dynamic_diff, requires_restart, ...}, restart_required}`; nothing is published or stored.
4. On explicit confirmation send the same request without `dry_run` and with a stable `Idempotency-Key` (16–128 chars `[A-Za-z0-9_-]`). Response `{operation, resource, restart_required}`; DELETE returns `{operation, restart_required}`. A network failure keeps the key and retries the same body (uncertain mode).
5. Static changes (resolvers, plugins) restart Traefik inside the operation. If that fails, the change is kept and the response is HTTP 202 with `error.code = ERR_MANUAL_RESTART_REQUIRED`: show the manual-restart notice. Config state then reports `restart_required`.
6. Operation `status`: `running` | `restoring` | `succeeded` | `restart_required` | `rolled_back` | `cleanup_pending` | `recovery_required` | `abandoned` — each needs its own presentation.
7. Errors: 428 missing precondition, 400 malformed, 409 `ERR_VERSION_CONFLICT` (reload the latest version) / `ERR_CONFIG_DRIFT` (adopt) / `ERR_CONFLICT` (operation in progress), 423 `ERR_OPERATION_RECOVERY_REQUIRED`, 422 validation (`field_errors`), 502 `ERR_TRAEFIK_UNAVAILABLE` (retryable), 507 `ERR_SNAPSHOT_QUOTA`, 410 `ERR_DRAFTS_REMOVED` (legacy draft routes).
8. Snapshots: `GET/POST /snapshots`, `GET/PATCH/DELETE /snapshots/{id}` (force for pinned), `GET /snapshots/{id}/config|resources`, `GET /snapshots/compare/{from}/{to}`, `GET /snapshots/{id}/restore-preview` → `restore_token`, `POST /snapshots/{id}/restore` `{restore_token, idempotency_key, skip_backup}` (`ERR_RESTORE_STALE` when the state moved). Operations: `GET /operations`, `GET /operations/{id}`, `POST /operations/{id}/recover|abandon`. State: `GET /config/state`, `POST /config/adopt`.

## 3. Endpoint → old page → response

| Area | Endpoints | Old page | Notes |
|---|---|---|---|
| Overview | `GET /overview` (camelCase: `traefikStatus, traefikVersion, routesLoaded/Total, certsExpiring30d, certificatesPartial, crowdSecStatus/Decisions, connected, isStale, observedAt`), `GET /entrypoints`, `GET /traefik/container`, `POST /traefik/restart`, `GET /traefik/logs` (restriction notice only) | `pages/Overview` | Show stale/unavailable explicitly; no fake metrics |
| Changes | `/snapshots*`, `/operations*`, `/config/state`, `/config/adopt` | `features/changes` (Snapshots, History, snapshot/restore/operation dialogs), shell banners | Snapshots and the operations log |
| Resolvers | `GET /resolvers/providers` → `{providers[8], custom_provider_codes}`, `GET/PUT/DELETE /resolvers/{name}` | `features/resolvers` | Values keyed by `env_var`; `alternative` = index into `auth_alternatives`; `type: acme|custom`; masked secrets `••••••••`; `external_credentials` |
| Domains | `GET /domains`, `PUT/DELETE /domains/{name}`, `POST /domains/{name}/probe` | `features/domains` | `method: resolver|files`; probe gives served-cert evidence (fingerprint/trust/validity separate) |
| Certificates | `GET /acme/certificates` → `{certificates[CertificateInfo], observations[], partial}`; `GET /certificates`, `PUT /certificates/{name}` `{purpose server|ca, cert_pem, key_pem?}` (4 MiB), `DELETE` | `features/certificates` (automatic / uploaded tabs) | PEM only in request; **no TLS options/stores** in core-v2 (old `TLSSettingsPage` has no equivalent) |
| Routers | `GET /routers`, `GET/PUT/DELETE /routers/{name}` | `features/routes` | `rule` computed from `domain_name/subdomain/path_prefix/custom_rule`; `tls{enabled,cert_resolver,domains,options}`; `middlewares[]` |
| Services | `GET /services`, `GET/PUT/DELETE /services/{name}`, `GET /services/{name}/diagnostics` (camelCase) | `features/services` | `servers[{url}]`, `health_check{path,interval,timeout}`, `pass_host_header`, `insecure_skip_verify`; load-balancer only |
| Middlewares | `GET /middlewares`, `PUT/DELETE /middlewares/{name}` `{type, spec, plugin_name?, secret_refs?}`; chains: `GET /chains`, `PUT/DELETE /chains/{name}` `{middlewares[]}` | `features/middlewares` | HTTP only (no TCP). `spec` free map (nulls allowed only here); `[redacted]` retains secret leaf. Old schema endpoint `/routing/middleware-schema` has no equivalent → reuse bundled form definitions |
| Plugins | `GET /plugins/catalog?q=`, `POST /plugins/catalog/sync`, `GET /plugins`, `PUT/DELETE /plugins/{name}` `{module_name,version}`, `POST /plugins/yaml` `{raw_yaml, short_name?, middleware_name?}` | `pages/Plugins`, `PluginInstall` | Catalog = metadata cache; install is staged → review/apply |
| CrowdSec | `GET/PUT /crowdsec/config` (**no envelope**; `lapi_key` masked), `GET/POST /crowdsec/decisions` (POST → 201, immediate), `DELETE /crowdsec/decisions/{ip...}` (encode each segment of `ip.split('/')`) | `pages/CrowdSec`, `CrowdSecWizard` | Decisions are live: no If-Match, no draft |
| Health | `GET /health` | – | `{status:"ok", authentication:"deferred"}` |

Not in core-v2 (pages to drop or mark unavailable): login/setup/settings-account, TLS options, TLS stores, TCP/UDP routers/services/middlewares, backups/adoption/inventory, raw logs.

## 4. Gotchas for the client
- Names are path segments: `encodeURIComponent`; keep `@docker`/`@file` suffixes; `read_only` objects (external) are view-only.
- `config` is the live value; there is no staged copy.
- `Resolver.rendered_node`, `Middleware.spec` are opaque maps.
- Sensitive inputs never in storage/URL; masks `••••••••` / `[redacted]` must be sent back unchanged to retain.
- Browser CORS allows only `Content-Type, Idempotency-Key, If-Match, If-None-Match`; use Vite proxy (same-origin) in dev.
- Mutations are never auto-retried; a retried write reuses the same body+key.

## 5. Proposed task order for frontend-v2
1. **Foundation** (this step): scaffold, design system, shell, API client. Next in it: `core/` typed client (`types.ts`, `mutations.ts` with the direct-write helpers, `normalize` for null lists), `App.svelte` + router, nav trimmed to supported sections.
2. Changes (snapshots / operations log / restore) — needed by every other page.
3. Resolvers → 4. Domains → 5. Certificates (ACME + imported) → 6. Services → 7. Routes → 8. Middlewares & chains → 9. Plugins → 10. CrowdSec → 11. Overview → 12. Settings (language/theme only).

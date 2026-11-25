# Traefik Console backend

This Go/SQLite backend manages Traefik files through one direct-operation engine (validate → render → journal → publish → verify → commit, one resource per request). A single administrator signs in with a password or an external OpenID Connect / OAuth 2.0 provider; see [Authentication](#authentication). The default listener is `127.0.0.1:8081`; host/origin policy is an additional boundary.

## Build and check

Use exactly Go 1.27.3. Dependencies retain the versions and checksums in `go.mod`/`go.sum`; the Makefile uses `GOPROXY=off`. Populate the module cache from these pins before an offline build (`GOPROXY=https://proxy.golang.org make deps`). Then run `make verify` and `make race` here. Output goes to the repository's ignored `.scratch/backend-build/`. `GOCACHE` and `OUTPUT` may be overridden. The release image embeds the SPA; see `deploy/`.

The API contract is `api/openapi.json` at repository root. Maintained generation/drift checks live in `tools/contract/`.

## Layer ownership

- `cmd/server` composes the server and offline validation/rollback CLI; Docker operations run through the restricted in-process handler.
- `internal/api` owns HTTP decoding, revision headers, bounded admission, error projection and redaction.
- API startup uses named dependencies and a bound static-read function. `internal/api` groups files into `handlers`, `request`, `response` and `middleware` packages, with explicit routes and no root forwarding facade; see `internal/api/README.md` for boundaries, compatibility and tests.
- `internal/core/<resource>` validates resource inputs and prepares batches through small read capabilities. Constructors are `constructor.go`; HTTP handlers stay in `api/handlers/`.
- The root core aggregation/getter/export layer is removed. Executable startup composes the resource packages directly into API dependencies; CLI rollback creates only its required CrowdSec verifier. See `internal/core/README.md`.
- `internal/engine` owns staging, graph checks, immutable review identity, the apply/recovery journal and observation decisions.
- `internal/db` owns SQLite mapping, checksummed migrations and encrypted recovery bundles. Configuration still lives in Traefik files; the database holds per-resource versions, user snapshots, the operations log, ownership, integration metadata and journal state.
- `internal/config` loads settings without filesystem effects. Its `files`, `filesystem`, `secrets` and `yaml` subpackages own explicit, grouped capabilities; executable startup owns the state lease/SQLite lifetime. See `internal/config/README.md`. `internal/traefik` owns schema validation and bounded observation.
- `internal/docker` implements narrow, fixed container operations directly over the configured Unix socket or existing TCP/HTTP socket proxy. Only `cmd/server` constructs it; consumers receive small typed capabilities. There is one executable and no custom broker transport. See `internal/docker/README.md`.
- `internal/traefik/status` separates pure static discovery (`DiscoverStatic`) from fresh protocol-scoped runtime observation (`ObserveRuntime`), convergence and registered ACME observation. Docker container inspection is a separate operation.
- `internal/core/plugins` owns plugin resources and catalog persistence; `internal/traefik/plugins` owns only fixed-endpoint catalog fetching/decoding. `internal/traefik/routing` owns rule compilation and domain binding.
- `internal/models` contains data types and methods intrinsic to those types. `internal/presentation` owns shared payload selection and timestamp formatting; resource-specific health, ownership and redaction stay with their existing owners.

## Authentication

`internal/auth` owns the domain and `internal/api/authgate` the HTTP boundary; `cmd/server` installs the gate as `middleware.Policy.Gate`. Credentials live in a separate private SQLite file (`auth.db`), never in the configuration database, so snapshot restore cannot touch them and session checks never wait behind a configuration operation.

- **First run.** While no administrator exists the server invalidates previous setup tokens at startup. Obtain a fresh one locally with `server admin setup-token`. The browser setup page requires it; `POST /api/v1/auth/setup` is refused with 409 once an administrator exists, and the token is deleted.
- **Command line** (works while the server runs; passwords are prompted without echo or read from a pipe, never from arguments):
  `server admin status` · `server admin create [--username NAME]` · `server admin reset-password` · `server admin setup-token` · `server admin reset --yes` (removes the administrator, sessions and external sign-in, back to first-run setup).
- **Sessions.** Argon2id (64 MiB, t=3) password hashes; 256-bit session tokens stored as SHA-256 in an HttpOnly SameSite=Lax cookie `tc_session`; idle and absolute expiry; a credential epoch ends every session on password change/reset. Every `/api/v1` route except `GET /api/v1/health` and the public `/api/v1/auth` endpoints needs the session; POST/PUT/PATCH/DELETE also need `X-CSRF-Token` with the session's `csrf` value. Sign-in endpoints accept only `application/json`. Failed attempts are rate-limited per client address (5 per 15 min) and per username (20 per 15 min).
- **External sign-in.** Any number of providers (up to 10); each one is a “Sign in with …” button on the sign-in page as soon as it is saved. OpenID Connect (discovery, ID token signature/issuer/audience/expiry/nonce verified with stdlib crypto; RS/PS/ES/EdDSA) or plain OAuth 2.0 with a user-info endpoint (for example GitHub). Authorization code + PKCE S256 + single-use state bound to the browser by a short-lived cookie. Only identities on the configured allow list sign in, as the administrator; an `email` explicitly marked unverified is refused. The client secret is sealed with a key derived from the master key. Adding, changing or removing a provider requires the current password; removal ends the sessions it opened. Single sessions can be signed out from the Security page. The callback (shared by all providers; the state identifies the provider) is `<console origin>/api/v1/auth/oauth/callback`.

| Variable | Purpose/default |
|---|---|
| `AUTH_DB_PATH` | Auth database, default `auth.db` beside `DB_PATH` |
| `COOKIE_SECURE` | Secure cookies; default true when `CONSOLE_ORIGIN` is https. Set true whenever browsers reach the console over https |
| `SESSION_IDLE_MINUTES`, `SESSION_MAX_HOURS` | 120 (5–10080) and 24 (1–720) |
| `TRUSTED_PROXIES` | Comma-separated IPs/CIDRs whose `X-Forwarded-For` is believed for rate limiting and the audit log (for example the Traefik container network) |

## Operator configuration

| Variable | Purpose/default |
|---|---|
| `BIND_ADDRESS`, `PORT` | `127.0.0.1`, `8081`; development API binding |
| `CONSOLE_HOST`, `CONSOLE_ORIGIN` | Optional exact allowed host (including port) and browser origin; loopback hosts allowed by default |
| `DB_PATH`, `MASTER_KEY_FILE` | `./data/console.db`, `./keys/master.key`; preserve the original key with the database |
| `MAX_JOURNAL_BYTES` | Encrypted journal payload cap, default 512 MiB, range 1 MiB–1 TiB; reserves finalization space, blocks rather than deleting history |
| `STATIC_FILE` | Traefik's static file as the Console sees it, default `./traefik/traefik.yml` (image: `/config/static/traefik.yml`) |
| `DYNAMIC_DIR`, `TRAEFIK_DYNAMIC_DIR` | The watched dynamic folder: inside the Console (default `./traefik/dynamic`, image `/config/dynamic`) and inside Traefik (default `/etc/traefik/dynamic`, must equal `providers.file.directory`) |
| `SECRETS_DIR`, `TRAEFIK_SECRETS_DIR` | The secrets folder: inside the Console (default `./traefik/secrets`, image `/config/secrets`) and inside Traefik (default `/etc/traefik/secrets`) |
| `TRAEFIK_API_URL` | Operator-registered observation origin, default `http://127.0.0.1:8080` |
| `TRAEFIK_CONVERGENCE_TIMEOUT_SEC` | Default 15, maximum 60 seconds; HTTP work also has a deadline |
| (ACME folder) | Not configurable: Traefik stores ACME files in `/etc/traefik/acme` (resolvers created by the Console point there), and the Console reads the same folder as `acme` next to `DYNAMIC_DIR`. Without that folder, ACME certificates are reported as not mapped |
| `TRAEFIK_PROBE_TARGETS` | Entrypoint/address pairs the domain "Check Traefik" probe connects to, default `websecure=127.0.0.1:443`. The address must reach Traefik's entry point **from the Console** (a published port, or the Traefik container name on a shared network). The probe sends SNI for a hostname one of the domain's routers serves. |
| `CROWDSEC_LAPI_URL` | Required to enable CrowdSec; empty leaves it unconfigured. Registered origin; request inputs cannot redirect credentials to another origin |
| `CROWDSEC_API_KEY`, `CROWDSEC_MACHINE_ID`, `CROWDSEC_MACHINE_PASSWORD` | Optional existing bouncer and separate dashboard machine credentials; saved Console credentials take precedence and remain encrypted |
| `CROWDSEC_CA_CERT` | Optional private LAPI CA certificate file |
| `CROWDSEC_BOUNCER_CERT`, `CROWDSEC_BOUNCER_KEY` | Optional bouncer mTLS certificate/key pair; roles are never shared |
| `CROWDSEC_MACHINE_CERT`, `CROWDSEC_MACHINE_KEY` | Optional dashboard machine mTLS certificate/key pair |
| `CROWDSEC_APPSEC_URL` | Optional registered AppSec origin used only when AppSec is enabled in the reviewed middleware configuration |
| `CROWDSEC_METRICS_URL`, `CROWDSEC_METRICS_TOKEN` | Optional protected `/metrics` endpoint and bearer token for measured LAPI/AppSec/ingestion metrics |
| `CROWDSEC_ACCESS_LOG_SOURCE` | Exact acquisition source label required before ingestion is reported as verified |
| `CROWDSEC_READ_TIMEOUT` | LAPI/metrics read timeout, default 20 seconds, clamped to 1–120 |
| `CROWDSEC_ALERT_LIMIT` | Maximum bounded native alert read, default 500, clamped to 1–1000; zero uses the bounded default, never unlimited |
| `CROWDSEC_PROTECTED_CIDRS` | Management networks protected from manual bans; default loopback only |
| `DOCKER_HOST` | Optional Docker Unix socket or TCP/HTTP(S) socket-proxy root endpoint; empty disables container operations |

CrowdSec uses an already configured engine; the Console never provisions one. Native LAPI reads use separate bouncer/machine credentials. Optional fixed container operations enroll both roles, create manual decisions and manage Console-owned allowlists. `GET /api/v1/crowdsec/status` reports desired configuration separately from LAPI, machine, ingestion, remediation and per-route state. `GET /stats?window=1h|24h|7d|30d` aggregates measured alerts, decisions and bouncers. `GET /alerts` and `/alerts/{id}` provide bounded native alerts; `DELETE /decisions/id/{id}` removes exactly one decision; legacy value deletion remains available for compatibility. `GET /allowlists` and its create/entry/check/delete operations cover local centralized allowlists, while only `traefik-console-` lists can be deleted. `GET /metrics` exposes only startup-registered measured metrics, and `GET /hub` returns read-only installed hub content. `PUT /crowdsec/config` reviews one M03 operation: existing valid resources are reused, reused middleware is disabled rather than deleted, and role-specific credentials are enrolled/rotated/revoked only when Console-owned. Alerts are detections, not request counts; base-bouncer visits and blocked-request totals are not inferred. AppSec processed/blocked counters are shown only as AppSec metrics.

Use a dedicated private state directory and private regular DB/key files. Existing databases require the original key; the application never silently replaces a missing key. Back up DB/key and live configuration coherently while stopped. Historical unsafe snapshots have metadata visibility only; they are not accepted for restore. No in-place compatibility promise is made for old unaudited snapshots.

Docker registration is operator-controlled: set `DOCKER_HOST` and the container names `TRAEFIK_CONTAINER` (default `traefik`) and optionally `CROWDSEC_CONTAINER`. Restart requires `ALLOW_TRAEFIK_RESTART=true`. The handler resolves each name on every operation, checks the container is exactly the registered one, and constructs fixed commands. The former `TRAEFIK_CONTAINER_ID`, `CROWDSEC_CONTAINER_ID`, `DOCKER_COMPOSE_PROJECT`, `TRAEFIK_COMPOSE_SERVICE` and `CROWDSEC_COMPOSE_SERVICE` fail with a message naming the replacement. Old `BROKER_*` settings fail with an explicit migration message. See `internal/docker/README.md` for transport, registration, proxy permissions and the intentionally changed process boundary.

The obsolete `TRAEFIK_ACME_PATH` option is rejected with a migration message. Remove it; the ACME folder is found next to the dynamic folder. The former names `TRAEFIK_STATIC_PATH` and `TRAEFIK_RUNTIME_DYNAMIC_DIR` are rejected with their new names, and `ACME_OBSERVATION_DIR`, `ACME_DIR` and `TRAEFIK_ACME_DIR` are rejected as no longer used. There is one registered ACME reader; the former arbitrary-file-path helper is removed.

Static discovery does not need the Traefik HTTP API. Runtime resource lists and dry-run previews require a fresh observation: an outage, malformed response or exhausted observation capacity returns retryable HTTP 502 `ERR_TRAEFIK_UNAVAILABLE`, rather than an apparently complete empty inventory. A failed preview changes nothing and publishes no files. Overview continues to report unavailable/stale explicitly. Public JSON layouts and API routes are unchanged.


## Client write protocol

There are no drafts. Each write is one operation that is validated, rendered, journalled, published, verified and committed in a single SQLite transaction. Operations are serialized; a concurrent one returns 409 `ERR_CONFLICT`.

1. Read the resource. Every resource carries an integer `version`.
2. Write it with `PUT /api/v1/<kind>/{name}` (or the matching POST/DELETE action). Send `If-Match: "<version>"` to change or delete, or `If-None-Match: *` to create. Neither header returns 428; a malformed value, or both headers, returns 400; a stale version returns 409 `ERR_VERSION_CONFLICT`. Resources that changed on disk since the last operation return 409 `ERR_CONFIG_DRIFT` until adopted with `POST /api/v1/config/adopt`.
3. Add `?dry_run=true` to any write to get the preview (`changes` with actions created, updated, deleted or impacted, plus the static and dynamic diffs and `requires_restart`) without publishing anything.
4. Send an `Idempotency-Key` header (16–128 alphanumeric, underscore or hyphen characters) on the real write. Replaying the same key and body returns the stored result, so a client that lost the response retries safely.
5. Changes to static configuration (resolvers, plugins) restart Traefik inside the operation. If the restart cannot run or fails, the change is kept and committed and the response is HTTP 202 with `ERR_MANUAL_RESTART_REQUIRED`: the operator must restart Traefik manually. The configuration state then reports `restart_required`.

Restore points are user snapshots (`GET/POST /api/v1/snapshots`, `PATCH/DELETE /api/v1/snapshots/{id}`; origins manual, pre_restore, pre_static, scheduled, legacy_import, adopted; pinnable and flaggable `known_good`). `GET /api/v1/snapshots/{id}/restore-preview` returns a `restore_token`; `POST /api/v1/snapshots/{id}/restore` with that token and an idempotency key restores it, taking a `pre_restore` backup first unless `skip_backup` is set. A stale token returns `ERR_RESTORE_STALE`, a pinned snapshot refuses deletion without force (`ERR_SNAPSHOT_PINNED`), and the snapshot quota returns 507 `ERR_SNAPSHOT_QUOTA`. `GET /api/v1/operations` lists the operations log; an operation left in recovery is resolved with `POST /api/v1/operations/{id}/recover` or `/abandon`, and until then writes return 423 `ERR_OPERATION_RECOVERY_REQUIRED`. The legacy draft routes answer 410 `ERR_DRAFTS_REMOVED`. Stop the manager before offline CLI rollback; the same state lease prevents concurrent writers.

`[redacted]` and `••••••••` retain an existing masked middleware leaf under the version check; they cannot create a new secret value. Resource responses preserve JSON shape but conceal credential-like fields and opaque plugin scalars. Raw logs and snapshot YAML are not API exports.

`working` means desired dynamic configuration was observed. It does not prove traffic health or issued certificates. Static changes that could not be verified, TLS imports and unconfirmed runtime reads use `saved_unverified`; a static change whose restart could not run uses `restart_required`. DNS credential mount changes may require operator recreation, rather than restart. `recovery_required` blocks edits until reconciliation succeeds; `cleanup_pending` retains durable owned CrowdSec cleanup for retry. Health is process liveness only.

## Scope and remaining acceptance

Exactly eight guided DNS providers are exposed: Cloudflare, OVH, DuckDNS, Namecheap, Google Cloud DNS, Azure DNS, Hetzner and DigitalOcean. All required values/options use audited `_FILE` mappings, with integer-second delivery for lego durations. Custom resolver YAML has bounded native ACME validation and externally configured credentials. The full upstream provider registry remains source material, not a claim of supported guided forms or live issuance verification.

Imported TLS validates key/chain/hostname bindings. ACME inventory reports configured storage observation and partial/invalid entries without returning private material. CrowdSec configuration/enrollment is transactional with explicit router attachment and proxy trust, but configured is not protected: this batch does not prove live ingestion/blocking or automatically provision engines/acquisition mounts. Dashboard machine enrollment, native alerts/decisions/allowlists, read-only hub inventory and optional AppSec/metrics flows are implemented. Native HTTP middleware validation is based on the committed Traefik v3.7.14 inventory; third-party settings remain opaque. Broader TCP/UDP/composite/transport/TLS-store parity and frontend adaptation are outside this batch.

Manual Traefik restart is serialized and blocked during recovery, but is not a durable idempotent configuration job. Journal quota is not a whole-database disk quota or an automatic pruning/export workflow. Runtime files and ordinary resource JSON in SQLite are not wholly encrypted; known managed secrets and recovery bundles are encrypted at rest. Protect the state directory. Full release, image/OS scans, ARM64/resource gates, public DNS issuance and live CrowdSec acceptance remain open. See `docs/architecture/core-v2-corrections.md` and the scoped review package.

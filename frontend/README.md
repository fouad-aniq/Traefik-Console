# Traefik Console frontend

Svelte 5 / TypeScript SPA for the Go backend in `../backend`. Direct-write protocol (one request = one operation, dry-run preview, per-resource versions) and on-demand snapshots.
See `docs/CORE-V2-API-ANALYSIS.md`.

Status:

- `src/core/`: core-v2 types and the shared direct-write helpers (`saveResource`/`deleteResource`: preview with `?dry_run=true`, then the same request with a stable `Idempotency-Key`; `If-Match "<version>"` to change, `If-None-Match: *` to create).
- `src/features/resolvers/`: **Resolvers page + create/edit/view dialog** (done).
- `src/features/certificates/`: **Certificates page** (automatic + uploaded) with upload/replace/delete dialogs (done).
- `src/features/domains/`: **Domains page** with add/edit/view dialogs, certificate matching and the Traefik check (done).
- `src/features/middlewares/`: **Middlewares and Chains pages** with schema-driven create/edit/view dialogs (form or YAML), plugin middlewares, ordered chains, usage and delete guards (done). Secret values return as `[redacted]`; sending the marker back keeps them.
- `src/features/services/`: **Services page** with create/edit/view dialogs (ordered backends http/https/h2c, health check, TLS verification) and a read-only runtime check (done).
- `src/features/routes/`: **Routes page** with create/edit/view dialogs (managed-domain, hostname or custom-rule matching, entrypoints, service, ordered middlewares/chains, TLS when not bound to a domain) (done).
- `src/features/changes/`: **Changes & Backups page** (done): a Snapshots tab (create, rename, pin, delete with force for pinned ones, search/filter/sort, keyset paging, compare two snapshots, restore through a preview token with an optional backup) and a History tab with the operations log (recover/abandon operations stuck in recovery). The shell banners show recovery-required, config drift (with Adopt), restart-required and archived legacy drafts, and a Create snapshot shortcut.
- `src/features/plugins/`: **Plugins page**: one management page (versions, server-computed update status from `GET /plugins`, edit, remove while unused). Add plugin is a single form with three sources: From catalog (the Module field becomes a searchable select that fills alias and a version list), Form, or YAML; updates stage the version you pick (done).
- `src/features/crowdsec/`: **CrowdSec operational console**: Overview, Alerts, Decisions, Allowlists and Integration tabs using the shared tables/badges/notices. A full-page setup wizard preserves existing `/setup`, `/disable` and `/rotate` links and supports automatic or manual role enrollment, failure policy and optional AppSec. Alert details use bounded native responses; unban uses exact decision IDs; allowlists use small existing dialogs and Console-owned deletion guards. Integration reports LAPI/machine, ingestion, remediation, metrics and read-only hub state independently—saved configuration is never presented as verified blocking.
- `src/features/topology/`: **Configuration map** (`#/map`, done): every object and its references as one left-to-right graph (resolvers / certificates → domains + entrypoints → routes → chains + middlewares → services; a chain is tied to its members by a dashed bracket). `graph.ts` is the pure model (nodes, edges, layered layout, link rules, PUT bodies); dragging one object onto another, or "Link to…" in the side panel, opens `ConnectDialog` (preview → apply on the owning resource); Edit / Add open the existing resource dialogs in place. Lines follow the request (route → its middlewares in order → service, or straight to the service when there are none; `pipelineEdges`), and services carry their backend protocol (HTTP / HTTPS / H2C). Missing references are drawn as broken objects.
- The map is the home page (`#/`); there is no Overview or Settings page, and unknown addresses show a not-found page.

Development:

    bun install --frozen-lockfile
    bun run dev   # proxies /api to http://127.0.0.1:8081 (CORE_V2_URL to override)

## Authentication

`App.svelte` asks `GET /api/v1/auth/status` first and shows exactly one of: setup (no administrator yet), sign-in, or the console. `features/auth/session.svelte.ts` holds the session in memory (the cookie is HttpOnly), adds `X-CSRF-Token` to every non-GET request through `ApiClient.setHeaderProvider`, and returns to sign-in when any protected call answers 401. Screens: `SetupPage` (one-time token from the local admin CLI → administrator account), `LoginPage` (password plus one “Sign in with …” button per configured provider; “Forgot password?” shows the reset command the server reports for this deployment — `docker exec …` when it runs in a container — with a copy button), and `security/SecurityPage` at `#/settings/security` with routed tabs like the other pages: Account (summary + change-password dialog), Sign-in providers (one card per provider with its button preview; the add/edit dialog has Google/GitHub/Okta fill-in buttons, a Type select and one field per line with an (i) info tip; saving and deleting ask for the password in a confirmation dialog), Sessions (table, sign out one or all others) and Activity (filterable table). During development set `CONSOLE_ORIGIN=http://localhost:5173` on the backend so the Vite-proxied requests pass the origin check, and register `http://localhost:5173/api/v1/auth/oauth/callback` at a test identity provider.

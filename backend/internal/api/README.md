# HTTP API

The API is grouped into four Go packages. Startup imports the constructor directly from `api/handlers`; there is no forwarding package, router framework, generic CRUD controller or dependency registry.

```text
api/
  handlers/     server construction, explicit routes, resource handlers and route tests
  request/      bounded JSON decoding and per-resource version headers (`If-Match`, `If-None-Match`)
  response/     response encoding, API errors/mapping and response recording
  middleware/   host/origin policy, admission, the authentication slot (Policy.Gate) and request logging
  authgate/     /api/v1/auth endpoints, session + CSRF enforcement for every other /api/v1 route
  README.md
```

- `handlers/server.go`: named `Dependencies`, explicit optional ACME observer and startup checks. Startup supplies `ReadStaticConfig: filesMgr.ReadStaticConfig`; the API receives no writable file repository. Interface values must contain valid implementations; no reflection-based typed-nil introspection is added.
- `handlers/router.go`: all 66 method/path registrations. `request.WithResourceVersion` parses `If-Match "<n>"` / `If-None-Match: *` for resource writes; the engine still compares versions under its lock. Writes accept `?dry_run=true` and an `Idempotency-Key`; restore carries its key in the body.
- `handlers/{runtime,snapshots,operations,resolvers,domains,certificates,routing,middlewares,plugins,crowdsec}.go`: existing thin resource handlers. The CrowdSec peer check uses `RemoteAddr`, not unregistered forwarded headers.
- `request/json.go`, `request/revision.go`: bounded typed decoding and supported revision forms. Only shared helpers/options are exported to handlers; parser internals stay private.
- `response/{json,errors,mapping,recorder}.go`: encode/redact before commit, immutable error projection, explicit domain-error mapping and the single response-status/operation-ID recorder.
- `middleware/{chain,policy,logging}.go`: startup policy, development actor, admission/deadline and recovery/completion logging. Middleware and response helpers share the recorder; the registered route pattern is recorded through `middleware.SetRoute`. No raw URL/body/panic content is logged.

Dependencies are acyclic: handlers use request, response and middleware; request and middleware use response; response does not import the other API packages. Tests move with their responsibilities and retain all 18 existing test identities. These are package/folder changes, not changes to authentication, transaction behavior or public HTTP contracts. Route tooling and internal schema ownership now follow the new paths.

## Request compatibility

Review ETags are quoted. Editing endpoints accept either the existing bare 64-hex candidate hash or one quoted strong tag in `If-Match`. Missing values return 428; malformed, weak, wildcard or multiple values return 400; a valid but stale hash still reaches the engine's 409 check. Unknown routes/methods use normal ServeMux 404/405 responses. Discard remains revision-protected; manual CrowdSec decisions remain outside draft-header policy.

JSON remains an object with strict typed fields, duplicate-key rejection, bounded UTF-8 input and maximum depth 32. Middleware `spec` may contain nested nulls; null envelope properties and a null `spec` object are rejected. Interrupted body reads return retryable 400, actual body-size overflow returns 413. Default body limit is 1 MiB; certificate import retains its 4 MiB body cap plus domain-level material bounds. Catalog query `q` is limited to 256 Unicode characters, matching the existing contract. Snapshot listing retains its fixed limit 25.

Content-Type rejection and exact-case struct-field enforcement remain deferred compatibility decisions. Existing case-insensitive Go struct binding and media-type tolerance are intentionally retained; arbitrary opaque map keys remain case-sensitive. No new pagination, JSON response envelope, authentication, provider/plugin scope or database schema is introduced.

## Verification

API tests cover failure/delegation for each route group, response serialization, shared-error immutability, panic before/after commit, request trace IDs, null/duplicate/alias/limit/depth/read errors, quoted/bare/malformed revision forms, normal routing errors, peer protection, host/origin/preflight, admission and cancellation. Test adapters deliberately return domain errors; real execution evidence belongs to the maintained Unix/TCP Docker lab. Full backend checks are `make verify` and `make race` from `backend`. Authentication lives in `api/authgate` (see the core-v2 README); `middleware.Chain` runs it after host/origin policy and admission. Without a `Policy.Gate` (isolated handler tests only) the explicit development actor is used.

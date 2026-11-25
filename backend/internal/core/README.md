# Application use cases

Each resource package owns its input validation, read projections, dependency checks and write preparation. There is no Go aggregation package at this directory root: callers import the owning resource package directly.

| Package | Responsibility |
|---|---|
| `resolvers` | Guided/custom resolver validation, provider registry and secret batches |
| `domains` | Domain preferences, bindings, usage and registered TLS probing |
| `certificates` | Imported certificate validation, public metadata and secret batches |
| `routers`, `services` | Routing/service use cases and observed external-resource projections |
| `middlewares` | Native/plugin middleware and chain use cases |
| `plugins` | Plugin declaration workflows and catalog persistence |
| `crowdsec` | Integration configuration, key verification and typed decision operations |

HTTP startup builds the existing `handlers.Dependencies` value in `cmd/server/composition.go`. It constructs resources directly, shares one registered ACME observer between domain and HTTP observation, and preserves constructor failures and the required transaction coordinator. There is no CoreConfig, resource getter container, re-export list or service locator. CLI rollback composes only its CrowdSec verifier alongside its existing transaction engine; it does not construct the HTTP resource set.

Resource-owned manager interfaces and error sentinels are authoritative. API response mapping imports those same sentinels, preserving `errors.Is` behavior. Local secret, observation and catalog interfaces remain small and independently testable. A static-read interface can live beside its constructor; it does not need a second root declaration. Five infallible constructors return only their manager; resolvers and CrowdSec retain error returns for registry/protected-network failures.

Domains and certificates reuse `presentation.DecodeUIConfig`, then perform their own usage/expiry enrichment. Live values are the only values; there is no staged projection. Shared helpers stay outside `models`.

The transaction engine still owns staged changes, locked revision comparison, apply/journal/recovery, publication, snapshots and rollback. Friendly resource checks do not replace its authoritative invariant checks. No resource endpoint writes live configuration directly. The broader identifier/list/staging-interface refactors remain separate follow-ups; no generic CRUD or callback framework is introduced. Authentication remains explicitly deferred for this candidate.

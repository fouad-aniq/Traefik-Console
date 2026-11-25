# Configuration code

`config.Load()` returns validated settings. It does not open SQLite, acquire a lease, create directories or load secret bytes. `Options.MasterKeyFile` is a path, not a key.

Docker endpoint/registration settings are operator values loaded here; `cmd/server` constructs the restricted handler directly. `BROKER_*` migration inputs fail explicitly.

The executable wires concrete services explicitly in `cmd/server/bootstrap.go`, `server.go` and `rollback.go`. The private startup resource owner closes only SQLite and its lease; it has no service getters and is never injected into business logic.

| Package | Responsibility |
|---|---|
| `files` | `Repository` binds registered static/dynamic paths. Static, managed dynamic and external inventory operations have separate files. |
| `filesystem` | Ordinary functions for bounded reads, path confinement, private atomic publication and leases. This is the only shared low-level file executor; there is no injected filesystem manager. |
| `secrets` | `Vault` prepares encrypted secret batches and reconciles registered runtime files. Key loading, crypto, secret batches, runtime publication, recovery and fingerprints are grouped separately. |
| `yaml` | Existing pure bounded parser/AST/renderer/diff helpers. |

Resource/API components declare read/preparation interfaces in their own packages. The generator receives read-only configuration access. The transaction engine alone receives configuration publication/restoration and secret runtime capabilities. Removing wrappers does not move OS writes into endpoints.

Startup order is settings/transport validation → state directory → exclusive lease → original key → SQLite → registered file/secret services → pending journal recovery → HTTP mutation admission. Healthcheck and offline YAML validation do not initialize state. Startup failure closes acquired resources; a newly initialized database/key is preserved rather than deleted.

`files.New(staticPath, dynamicDir)` and `secrets.New(managerDir, traefikDir, key, secretReader)` return concrete implementations. The caller supplies both directory views explicitly. No automatic key rotation, changed configuration paths or new credential delivery convention is introduced.

Tests cover missing versus empty files, external-file preservation, key reuse/unsafe-key rejection, encrypted non-persisting secret preparation, conflict-aware secret restoration and startup ordering/cleanup. The real lab additionally exercises resolver validation/staging/mount rejection/static publication, HTTP routing, rollback and served TLS fingerprints. Public DNS issuance and live CrowdSec protection are separate acceptance gates.

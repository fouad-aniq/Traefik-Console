# Core-v2 contract tools

These checks are scoped to `backend`; they do not regenerate legacy/recovered contracts. Use the exact Go 1.27.3 toolchain and pinned module cache.

From repository root, with a writable `GOCACHE`:

```sh
go run tools/contract/inspect_models.go backend > api/schemas/models.json
python3 tools/contract/generate_contract.py
python3 tools/contract/check_contract.py
.cache/api-contract-venv/bin/python tools/contract/validate_contract.py
```

`inspect_models.go` parses source AST and records explicit public JSON fields; hidden ciphertext/state fields are excluded. `generate_contract.py` combines reviewed route/request/response bindings with those fields. `check_contract.py` checks method/path equality, DTO field equality, the eight-provider allowlist explicit authentication deferral, and that the server uses the restricted Docker handler directly, the custom broker process/transport is absent, and raw command/log interfaces are not reintroduced. It does not replace runtime schema/behavior tests or a standards validator. Review generated changes and update provenance hashes deliberately. Standards validation uses `openapi-spec-validator` from the repository's pinned API-contract tooling environment; no new Python dependency is selected by these scripts.

`contract_details.py` supplies typed resource envelopes, separate editable input
projections, response serialization rules and reviewed operation semantics.
`contract_examples.py` holds synthetic examples, not runtime evidence or usable
credentials. `check_contract.py` also rejects generated OpenAPI drift.
`validate_contract.py` checks OpenAPI standards, deterministic generation,
schema/media examples, concrete configs and negative payloads using the existing
pinned validator environment. It does not claim semantic/runtime acceptance of
illustrative PEM/plugin placeholders.

Run the existing disposable integration lab with `CORE_V2_VALIDATE_CONTRACT=1`
and the same venv Python to validate actual manager JSON responses in memory.
Coverage records only method/template/status, never response payloads. See the
[frontend guide](../../docs/architecture/core-v2-frontend-integration.md) for
commands, lifecycle semantics and limits. New dependencies are not required.

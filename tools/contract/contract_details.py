"""Reviewed frontend semantics layered over the Go field/route inventory.

Request projections stay distinct from serialized response DTOs. Runtime validation
also checks references, provider alternatives and native Traefik configuration.
"""
from copy import deepcopy

RESOURCE_KINDS = {
    'resolvers': 'Resolver', 'domains': 'Domain', 'certificates': 'Certificate',
    'routers': 'Router', 'services': 'Service', 'middlewares': 'Middleware',
    'chains': 'Chain', 'plugins': 'Plugin', 'entrypoints': 'EntryPoint',
}
INPUT_REQUIRED = {
    'Resolver': ['type'], 'Domain': ['main', 'method'], 'Router': ['service'],
    'Service': ['servers'], 'Middleware': ['type', 'spec'],
    'Chain': ['middlewares'], 'Plugin': ['module_name', 'version'],
    'CrowdSecConfig': ['enabled'],
    'ServerEndpoint': ['url'], 'ServiceHealth': [], 'RouterTLS': [],
    'TLSDomain': ['main'], 'Propagation': [],
    'EntryPoint': ['port'], 'EntryPointForwardedHeaders': [], 'EntryPointProxyProtocol': [],
    'EntryPointTransport': [], 'EntryPointHTTP': [], 'EntryPointRedirect': ['to'],
    'EntryPointTLS': [], 'EntryPointTLSDomain': ['main'], 'EntryPointHTTP2': [],
    'EntryPointHTTP3': [], 'EntryPointUDP': [], 'EntryPointObservability': [],
}
COMPUTED = {
    'Resolver': ['name', 'storage', 'rendered_node', 'warnings'],
    'Domain': ['name', 'state', 'used_by'], 'Router': ['rule'],
    'EntryPoint': ['name', 'address', 'original', 'adoptable', 'adopt_blocker', 'usage', 'warnings', 'rendered_node'],
    'CrowdSecConfig': ['state', 'active_enabled', 'enrollment_ids', 'machine_enrollment_id', 'middleware_owned'],
}


def ref(name):
    return {'$ref': '#/components/schemas/' + name}


def object_schema(properties, required=None):
    return {'type': 'object', 'properties': properties,
            'required': list(properties) if required is None else required,
            'additionalProperties': False}


def enrich(spec, models):
    schemas = spec['components']['schemas']
    # Projection occurs before nullable Go collections and response-required fields.
    input_types = set(INPUT_REQUIRED)
    for name in sorted(input_types):
        schema = deepcopy(schemas[name])
        schema.pop('x-go-type', None)
        schema['required'] = INPUT_REQUIRED.get(name, [
            k for k, v in models[schemas[name]['x-go-type']].items() if not v['optional']])
        for field in COMPUTED.get(name, []):
            schema['properties'].pop(field)
        schema['description'] = ('Editable input. Omit computed response fields. '
                                 'Server validation also checks semantics and references.')
        schemas[name + 'Input'] = schema

    def request_refs(value):
        if isinstance(value, dict):
            target = value.get('$ref', '').rsplit('/', 1)[-1]
            if target in input_types:
                value['$ref'] = ref(target + 'Input')['$ref']
            for child in value.values():
                request_refs(child)
        elif isinstance(value, list):
            for child in value:
                request_refs(child)

    for name in input_types:
        request_refs(schemas[name + 'Input'])
    for name, schema in list(schemas.items()):
        go_type = schema.get('x-go-type')
        if not go_type:
            continue
        fields = models[go_type]
        schema['required'] = [k for k, v in fields.items() if not v['optional']]
        for field, meta in fields.items():
            # encoding/json emits null for nil non-omitempty slices/maps.
            prop = schema['properties'][field]
            if not meta['optional'] and meta['type'].startswith(('[]', 'map[')):
                prop['type'] = [prop['type'], 'null']
    # The wire view embeds the cached row. The AST inventory records its own
    # fields separately, so explicitly compose the flattened JSON projection.
    catalog = schemas['CatalogPlugin']
    catalog.pop('x-go-type')
    for field, meta in models['internal/models.CatalogPluginView'].items():
        catalog['properties'][field] = ({'type':'array','items':{'type':'string'}} if meta['type']=='[]string' else {'type': 'boolean' if meta['type'] == 'bool' else 'string'})
        if not meta['optional']:
            catalog['required'].append(field)
    catalog['properties']['install_state']['enum'] = ['not_installed', 'up_to_date', 'update_available', 'removal_pending', 'unknown']
    for kind, fields in COMPUTED.items():
        for field in fields:
            schemas[kind]['properties'][field]['readOnly'] = True
    for kind in RESOURCE_KINDS.values():
        schema = deepcopy(schemas['UIResource'])
        schema.pop('x-go-type')
        schema['properties']['config'] = ref(kind)
        schema['description'] = (
            f'{kind} with ownership and its committed configuration. version is the '
            'optimistic-concurrency counter to send back in If-Match.')
        schemas[kind + 'Resource'] = schema

    schemas['UIResource']['properties']['origin']['enum'] = ['managed', 'external']
    schemas['UIResource']['description'] = 'Generic envelope; endpoints use concrete resource schemas.'
    # WriteJSON runs generic key-based redaction after Go marshaling. The current
    # wire format therefore differs from Field.Secret's Go bool. Do not silently
    # describe the desired DTO instead of the real HTTP contract.
    schemas['DNSField']['properties']['is_secret'] = {
        'type': 'string', 'const': '[redacted]',
        'description': ('Known output-redaction defect: is_secret boolean metadata is '
                        'masked by key name. Use type == secret for current guided '
                        'forms; fixing metadata redaction is a separate backend task.'),
        'x-go-field-type': 'bool', 'x-wire-transform': 'key-based redaction',
    }
    for kind in RESOURCE_KINDS.values():
        schemas[kind + 'Resource']['properties']['origin']['enum'] = ['managed', 'external']
    entry = schemas['EntryPointInput']['properties']
    entry['protocol'].update(enum=['tcp', 'udp'], default='tcp')
    entry['kind'].update(enum=['http', 'https', 'tcp', 'udp'])
    entry['kind']['description'] = (
        'What the entrypoint is for. It must agree with the options: udp listens on UDP and the '
        'others on TCP; https requires http.tls and http forbids it; tcp (a plain TCP service) '
        'accepts no http, http2, http3, forwarded_headers or allow_acme_bypass. Omitted, it is '
        'derived from the settings. Never written to traefik.yml.')
    entry['port'].update(minimum=1, maximum=65535)
    entry['adopted']['description'] = (
        'Send true to take over an entrypoint that is declared in traefik.yml (adoptable). Its '
        'original definition is kept and comes back when the entrypoint is deleted or an older '
        'snapshot is restored. Ignored for a console-created entrypoint.')
    schemas['EntryPointInput']['description'] = (
        'A listener of traefik.yml (Traefik v3.7). Only values that differ from Traefik defaults are '
        'written. protocol is fixed at creation. A udp entrypoint accepts only host, port, reuse_port '
        'and udp.timeout; a tcp one accepts everything except udp. http3 also takes the UDP port of '
        'the same number. Trusting every source (insecure) excludes a trusted_ips list. redirect.to '
        'is another tcp entrypoint or :port. Every write changes traefik.yml and restarts Traefik.')
    schemas['EntryPointRedirectInput']['properties']['scheme']['enum'] = ['https', 'http']
    schemas['EntryPointHTTPInput']['properties']['alias_headers_strategy']['enum'] = ['keep', 'delete', 'reject']
    schemas['EntryPointHTTPInput']['properties']['denied_encoded_characters']['items']['enum'] = [
        'slash', 'backSlash', 'nullCharacter', 'semicolon', 'percent', 'questionMark', 'hash']
    schemas['EntryPointObservabilityInput']['properties']['trace_verbosity']['enum'] = ['minimal', 'detailed']
    schemas['EntryPointHTTP3Input']['properties']['advertised_port'].update(minimum=1, maximum=65535)
    schemas['ResolverInput']['properties']['type']['enum'] = ['acme', 'custom']
    resolver_props = schemas['ResolverInput']['properties']
    resolver_props['challenge']['enum'] = ['http01', 'tlsalpn01', 'dns01']
    resolver_props['environment']['enum'] = ['staging', 'production', 'custom']
    resolver_props['alternative']['minimum'] = 0
    resolver_props['values']['description'] = (
        'Keys are exact discovery field env names, not labels. Selected provider '
        'alternative required fields and optional fields are validated server-side. '
        'Secret values are masked on reads; the existing •••••••• marker retains '
        'an already stored value, and cannot create a missing credential.')
    resolver_props['external_credentials']['description'] = (
        'Use operator-provided DNS credentials; values is ignored. Discovery of a '
        'custom provider code does not imply a supported guided form.')
    schemas['ResolverInput']['description'] = (
        'acme: valid account email and challenge required; dns01 uses provider '
        'discovery, http01 needs a registered entry_point, tlsalpn01 needs '
        'public reachability on port 443. Environment defaults to production; choose staging explicitly '
        'for testing. custom: bounded custom_yaml, with external_credentials '
        'acknowledgment for DNS; no inline YAML secrets. Storage is server-owned.')
    def condition(properties, required):
        return {'if': {'properties': {key: {'const': value} for key, value in properties.items()},
                       'required': list(properties)}, 'then': {'required': required}}
    schemas['ResolverInput']['allOf'] = [
        condition({'type': 'custom'}, ['custom_yaml']),
        condition({'type': 'acme'}, ['email', 'challenge']),
        condition({'type': 'acme', 'challenge': 'dns01'}, ['provider']),
        condition({'type': 'acme', 'challenge': 'http01'}, ['entry_point']),
        condition({'type': 'acme', 'environment': 'custom'}, ['ca_url']),
    ]
    schemas['DomainInput']['properties']['method']['enum'] = ['resolver', 'files']
    schemas['DomainInput']['description'] = (
        'Domain preference: main and method; resolver method needs resolver, files '
        'method needs certificate. Resolver-backed routers request their own hostnames; '
        'domain SANs are optional explicit bundles, not a future-router allowlist. '
        'This alone does not create a router or prove issuance.')
    schemas['DomainInput']['allOf'] = [
        condition({'method': 'resolver'}, ['resolver']),
        condition({'method': 'files'}, ['certificate']),
    ]
    for name in ('Domain', 'DomainInput'):
        schemas[name]['properties']['entry_point'].update(
            deprecated=True, description='Compatibility probe target selector only; router entry_points owns routing. Not a domain routing entrypoint.')
    schemas['OperationPreview']['properties']['domain_diff']['description'] = 'Redacted Console-owned domain YAML diff, outside the Traefik watched directory.'
    schemas['RouterInput']['description'] = (
        'HTTP router input. Host/subdomain IDNA, paths and references are canonicalized. '
        'entry_points must be registered TCP listeners: omitted values use registered '
        'asDefault listeners, then an existing web/websecure name, then the sole TCP '
        'listener; ambiguous configurations require explicit selection. domain_name '
        'owns Host/TLS settings and overrides manual domains/tls; custom_rule still '
        'wins and TLS host coverage uncertainty is exposed in review warnings. '
        'Resolver-backed domain bindings automatically request the canonical router '
        'hostname without parent SAN edits. Explicit domain wildcard/SAN bundles '
        'are retained; a separate leaf request covers hosts outside that bundle. '
        'Imported certificates and manually supplied explicit TLS request names '
        'must still cover the bound host. Middleware order/repeats and provider '
        'identities are preserved.')
    schemas['RouterInput']['properties']['tls']['description'] = 'Manual TLS mode only; ignored when domain_name supplies TLS. Resolver must exist, wildcard requests require DNS-01.'
    schemas['RouterInput']['properties']['extra_domains']['description'] = 'More hostnames for a router bound with domain_name: optional subdomains of other managed domains. The rule matches every hostname; hostnames certified by a resolver must share the main hostname\'s resolver, hostnames with uploaded certificates must be covered by them.'
    schemas['RouterInput']['properties']['entry_points']['description'] = 'Registered TCP entrypoint names; UDP-only and unknown names are invalid for HTTP routers.'
    schemas['OperationPreview']['properties']['validation_warnings']['description'] = 'Nonblocking native warnings and advanced-rule TLS coverage uncertainty; never treat these as issuance/serving proof.'
    schemas['RouterResource']['description'] += ''
    schemas['ServiceInput']['properties']['servers']['minItems'] = 1
    schemas['ServerEndpointInput']['properties']['url']['description'] = (
        'http, https or h2c URL with a host; no user information, query or fragment.')
    schemas['ServiceHealthInput']['properties']['path']['default'] = '/'
    for field in ('interval', 'timeout'):
        schemas['ServiceHealthInput']['properties'][field]['description'] = (
            'If nonempty: positive Go duration, at most one hour.')
    schemas['CrowdSecConfigInput']['properties']['lapi_key']['description'] = (
        'Credential input; reads mask it as ••••••••. Retain an existing credential '
        'using the returned marker. Never store this input in browser persistence.')
    schemas['CrowdSecConfigInput']['properties']['machine_password']['description'] = (
        'Separate dashboard machine credential. Reads mask it; the retain marker preserves the encrypted stored value. '
        'It is used only by the Console backend and is never mounted into Traefik or returned as plaintext.')
    schemas['CrowdSecConfigInput']['properties']['update_max_failure']['description'] = 'Pinned bouncer failure policy: -1 never blocks solely because updates fail; 0 blocks on the first update failure; positive values bound consecutive failures.'
    schemas['CrowdSecConfigInput']['properties']['appsec_failure_block']['description'] = 'Whether AppSec unexpected/failure responses block requests; defaults true and is tested separately from LAPI outage policy.'
    schemas['CrowdSecConfigInput']['properties']['appsec_unreachable_block']['description'] = 'Whether an unreachable AppSec endpoint blocks requests; defaults true.'
    schemas['UploadCertificate']['description'] = (
        'Paste PEM material, never filesystem paths. purpose defaults to server; '
        'server requires the matching private key; ca accepts CA certificate material. '
        'The response contains metadata, never key PEM. Total JSON body limit 4 MiB.')

    simple = {
        'Deleted': {'deleted': {'const': True}},
        'Discarded': {'discarded': {'const': True}},
        'DecisionAdded': {'added': {'const': True}},
        'Created': {'created': {'const': True}},
        'Removed': {'removed': {'const': True}},
        'AllowlistCheck': {'allowlisted': {'type': 'boolean'}, 'reason': {'type': 'string'}},
        'Restarted': {'status': {'const': 'restarted'}},
        'CatalogSync': {'synced_count': {'type': 'integer', 'minimum': 0}},
        'EntryPoints': {
            'entry_points': {'type': ['array', 'null'], 'items': {'type': 'string'}},
            'protocols': {'type': 'object', 'additionalProperties': {'type': 'string', 'enum': ['tcp', 'udp']}},
            'items': {'type': ['array', 'null'], 'items': ref('EntryPointResource')}},
        'LogNotice': {'logs': {'type': 'string'}},
        'ProviderDiscovery': {
            'providers': {'type': 'array', 'items': ref('DNSProvider')},
            'custom_provider_codes': {'type': 'array', 'items': {'type': 'string'}}},
    }
    schemas.update({name: object_schema(props) for name, props in simple.items()})
    special = {
        ('get', 'health'): ('Check manager availability', 'Manager process availability; public, not Traefik traffic health.', None),
        ('get', 'overview'): ('Observe runtime overview', 'Measured runtime observation; fields use camelCase. Unavailable/stale state must remain visible.', 'Overview'),
        ('get', 'entrypoints'): ('List entrypoints', 'entry_points and protocols name every entrypoint of traefik.yml for router and HTTP-01 forms. items carries the full resources: managed ones are editable, the ones only declared in traefik.yml are read-only and say whether they can be adopted.', 'EntryPoints'),
        ('get', 'traefik/container'): ('Inspect registered Traefik', 'Sanitized metadata for the operator-registered role only; no arbitrary container target.', 'ContainerStatus'),
        ('post', 'traefik/restart'): ('Restart registered Traefik', 'Immediate serialized lifecycle action. A recovery_required operation can block it. Clears a pending manual restart on success.', 'Restarted'),
        ('get', 'traefik/logs'): ('Read log restriction notice', 'Returns a notice; raw container logs are restricted to operator tooling.', 'LogNotice'),
        ('get', 'snapshots'): ('List snapshots', 'Keyset-paginated restore points, newest first (limit 1-100, default 25; cursor from next_cursor). Metadata only: no YAML or secrets.', 'SnapshotPage'),
        ('post', 'snapshots'): ('Create snapshot', 'Captures the sealed state and the exact bytes of the three managed files under a name. Returns 201. 507 ERR_SNAPSHOT_QUOTA when the size limit would be exceeded.', 'Snapshot'),
        ('get', 'snapshots/{id}'): ('Read snapshot metadata', 'Metadata for one snapshot.', 'Snapshot'),
        ('patch', 'snapshots/{id}'): ('Rename, describe or pin a snapshot', 'Partial update of name, description and pinned. Pinned snapshots are never pruned.', 'Snapshot'),
        ('delete', 'snapshots/{id}'): ('Delete snapshot', 'Deletes the snapshot. A pinned snapshot returns 409 ERR_SNAPSHOT_PINNED unless force=true.', 'Deleted'),
        ('get', 'snapshots/{id}/config'): ('Read saved configuration', 'Read-only redacted YAML of the three managed files as saved in the snapshot, independent of current files and runtime. Credentials, opaque plugin/header values and URL userinfo/query/fragment are masked; a document that cannot be safely parsed within bounds returns [configuration unavailable], never raw text. Cache-Control: no-store. Display only, never reapply masked YAML. Invalid ID: 422; missing snapshot: 404.', 'SnapshotConfig'),
        ('get', 'snapshots/{id}/resources'): ('List resources in a snapshot', 'Kind and name of every resource stored in the snapshot.', 'SnapshotResource'),
        ('get', 'snapshots/{id}/restore-preview'): ('Preview a restore', 'Computes what restoring the snapshot would add, change and remove, plus diffs, validation results, env mounts and whether a Traefik restart is needed. restore_token must be sent to restore and expires when the configuration changes (409 ERR_RESTORE_STALE).', 'OperationPreview'),
        ('post', 'snapshots/{id}/restore'): ('Restore a snapshot', 'Applies the previewed restore as one operation, after taking a pre_restore backup snapshot (unless skip_backup). 409 ERR_CONFIG_DRIFT when files changed outside the console and a backup would be taken. When Traefik could not be restarted the result is 202 with ERR_MANUAL_RESTART_REQUIRED and the restore is kept.', 'RestoreResult'),
        ('get', 'snapshots/compare/{from}/{to}'): ('Compare two snapshots', 'What changed going from snapshot from to snapshot to, as a preview-shaped result.', 'OperationPreview'),
        ('get', 'operations'): ('List operations', 'Keyset-paginated operations log, newest first. This is the audit and recovery trail of every direct write, restore and restart.', 'OperationPage'),
        ('get', 'operations/{id}'): ('Read operation', 'One operation with its status, stage, safe reason code and error_log.', 'Operation'),
        ('post', 'operations/{id}/recover'): ('Recover operation', 'Retries compensation for an operation in recovery_required. 409 ERR_OPERATION_NOT_RECOVERABLE for other statuses.', 'Operation'),
        ('post', 'operations/{id}/abandon'): ('Abandon operation', 'Marks a recovery_required operation as abandoned so writes can resume; use only after checking the files by hand.', 'Operation'),
        ('get', 'config/state'): ('Read configuration state', 'Published versus on-disk hashes of the three managed files, drift, pending restart and the operation that needs recovery, if any.', 'ConfigState'),
        ('post', 'config/adopt'): ('Adopt on-disk configuration', 'Accepts the current on-disk files as the published baseline after an out-of-band edit. Recorded as an adopted snapshot and an operation.', 'Operation'),
        ('get', 'resolvers/providers'): ('Discover DNS form fields', 'Exactly eight guided providers and their credential alternatives. custom_provider_codes is source inventory, not guided support or live verification.', 'ProviderDiscovery'),
        ('get', 'acme/certificates'): ('Observe ACME certificates', 'Read-only registered storage observation. partial and per-resolver states distinguish empty/missing/unreadable storage from observed certificates; no PEM or account data.', 'ACMEInventory'),
        ('post', 'domains/{name}/probe'): ('Probe registered domain TLS', 'Read-only TLS handshake using a registered target. No arbitrary URL/address input. Compare observed fingerprint and trust separately.', 'DomainProbe'),
        ('get', 'services/{name}/diagnostics'): ('Observe service diagnostics', 'Read-only Traefik runtime observation, not an arbitrary backend URL probe; backend URLs are omitted.', 'ServiceDiagnostic'),
        ('get', 'plugins/catalog/page'): ('Read paginated cached catalog', 'Read cached catalog items with matching total, limit, offset and catalog-wide last_synced_at (empty before first refresh; UTC SQLite timestamp). q searches display_name/module_name/summary; module resolves an exact import path and is mutually exclusive with q. limit1..100 default25, offset0..1000000 default0, order asc/desc by display_name then module_name. No refresh or install. Use this for tables and installation prefill; legacy catalog array remains capped500. Configuration statuses do not establish runtime loading; catalog snippets are optional editable examples, never automatically staged.', 'CatalogPage'),
        ('get', 'plugins/catalog'): ('Search cached plugin catalog', 'Search catalog metadata with q (up to 256 Unicode characters). Catalog presence does not prove installed code is trusted or active.', 'CatalogPlugin'),
        ('post', 'plugins/catalog/sync'): ('Refresh plugin catalog', 'Refresh the fixed official website catalog API cache, with a persisted one-minute manual throttle. A throttled call returns the current count without fetching. A cancellable daily worker runs only after the first explicit refresh, with fifteen-minute failure backoff. Uses upstream import paths and YAML examples. Network/HTTP/schema/empty-unusable responses preserve cached entries and return 502 ERR_PLUGIN_CATALOG_UNAVAILABLE (retryable; optional field_errors.upstream_status). Does not install a plugin.', 'CatalogSync'),
        ('post', 'plugins/yaml'): ('Install plugin from YAML snippet', 'Saves a pinned static plugin declaration and optional middleware atomically. Traefik is restarted inside the operation; if that fails the change is kept and the response is 202 ERR_MANUAL_RESTART_REQUIRED. Not arbitrary YAML file execution.', 'PluginWriteResult'),
        ('get', 'crowdsec/config'): ('Read CrowdSec configuration', 'Plain configuration object, without a resource envelope. Credentials are masked; desired enabled and observed active_enabled differ.', 'CrowdSecConfig'),
        ('put', 'crowdsec/connection'): ('Save CrowdSec connection settings', 'Saves how the Console reaches CrowdSec (LAPI address and credentials, AppSec and metrics endpoints, access-log source, protected networks, read limits) and keeps the protection unchanged. A setting whose CROWDSEC_* variable is set is read-only and the submitted value is ignored. generate_key registers a new bouncer key through the registered CrowdSec container. If-Match uses CrowdSecConfig.version.', 'CrowdSecConfigWriteResult'),
        ('post', 'crowdsec/connection/test'): ('Test CrowdSec connection settings', 'Probes candidate settings without saving them: LAPI reachability and key, machine login, AppSec reachability and metrics. Locked settings use the environment values.', 'CrowdSecConnectionTest'),
        ('put', 'crowdsec/config'): ('Save CrowdSec configuration', 'Saves the configuration; If-Match uses CrowdSecConfig.version (If-None-Match: * for first save). Does not prove live blocking.', 'CrowdSecConfigWriteResult'),
        ('get', 'crowdsec/decisions'): ('List CrowdSec decisions', 'Read live decisions via the registered CrowdSec role.', 'CrowdSecDecision'),
        ('post', 'crowdsec/decisions'): ('Add CrowdSec decision', 'Immediate action outside the configuration. Requires ip or CIDR; duration is a positive Go duration at most seven days. Protected networks and connected peer are restricted. No If-Match.', 'DecisionAdded'),
        ('delete', 'crowdsec/decisions/{ip}'): ('Delete CrowdSec decision by value', 'Legacy immediate deletion for an IP/CIDR. Prefer deletion by exact decision ID to avoid removing multiple matching decisions. No If-Match.', 'Deleted'),
        ('delete', 'crowdsec/decisions/id/{id}'): ('Delete CrowdSec decision by ID', 'Immediately deletes exactly one decision through machine authentication or the registered CrowdSec container. No If-Match.', 'Deleted'),
        ('get', 'crowdsec/alerts'): ('List CrowdSec alerts', 'Bounded alert inventory with zero-based page (0-9), limit (1-100, default 25), since_hours (1-720, default 24), optional scenario/source/origin filters, and honest has_more/observed_at. Native machine authentication is preferred; the registered container is a compatibility fallback.', 'CrowdSecAlertPage'),
        ('get', 'crowdsec/alerts/{id}'): ('Read CrowdSec alert', 'Safe alert details by exact ID, without raw log payloads. Missing alert: 404; missing machine/container observation capability: 503.', 'CrowdSecAlert'),
        ('get', 'crowdsec/metrics'): ('Read measured CrowdSec metrics', 'Reads only a startup-registered /metrics endpoint. LAPI requests, bouncer pulls, AppSec counters and CAPI freshness are native counters since engine start. Ingestion is verified only for the exact registered access-log source; otherwise it remains unverified and its value is null.', 'CrowdSecMetricsSnapshot'),
        ('get', 'crowdsec/hub'): ('List installed CrowdSec hub items', 'Read-only collections, parsers, scenarios, postoverflows, AppSec configurations/rules and contexts through the fixed registered container operation. No install, removal, update or arbitrary command is exposed.', 'CrowdSecHubItem'),
        ('get', 'crowdsec/allowlists'): ('List CrowdSec allowlists', 'Read centralized allowlists and bounded entries through machine authentication or the registered CrowdSec container.', 'CrowdSecAllowlist'),
        ('post', 'crowdsec/allowlists'): ('Create CrowdSec allowlist', 'Creates a Console-owned allowlist through the fixed registered container operation. Name must begin traefik-console-. No arbitrary CLI or filesystem input.', 'Created'),
        ('post', 'crowdsec/allowlists/check'): ('Check CrowdSec allowlist membership', 'Checks one IP/CIDR using native machine authentication or the fixed registered container operation. An unavailable result is never presented as not allowlisted.', 'AllowlistCheck'),
        ('post', 'crowdsec/allowlists/{name}/entries'): ('Add CrowdSec allowlist entry', 'Adds one IP/CIDR with optional comment and expiration through the fixed registered container operation.', 'DecisionAdded'),
        ('delete', 'crowdsec/allowlists/{name}/entries'): ('Remove CrowdSec allowlist entry', 'Removes exactly the IP/CIDR supplied in the strict JSON body from the named allowlist.', 'Removed'),
        ('delete', 'crowdsec/allowlists/{name}'): ('Delete Console-owned allowlist', 'Deletes an allowlist only when its name has the Console-owned traefik-console- prefix. External lists cannot be deleted through this operation.', 'Deleted'),
    }
    tags = set()
    special[('post', 'plugins/{name}/update')] = (
        'Update cataloged plugin',
        'Requires If-Match and a managed plugin. Body version is required. '
        'Applies exactly the requested known version as a static change: Traefik is '
        'restarted inside the operation, or 202 ERR_MANUAL_RESTART_REQUIRED is returned '
        'when the restart fails and the change is kept. '
        'Returns ERR_PLUGIN_NO_UPDATE when no newer comparable version exists, or '
        'ERR_PLUGIN_NOT_IN_CATALOG when the module has no cached release.',
        'PluginWriteResult')
    view = deepcopy(schemas['PluginResource'])
    view['properties']['update'] = ref('PluginUpdateInfo')
    view['required'] = sorted(view['required'] + ['update'])
    view['description'] = 'PluginResource plus its update status against the cached catalog, computed on every read.'
    schemas['PluginView'] = view
    schemas['PluginUpdateInfo']['required'] = ['state', 'versions', 'can_update', 'last_attempt_failed']
    schemas['PluginUpdateInfo']['properties']['state']['enum'] = ['up_to_date', 'update_available', 'not_in_catalog', 'unknown']
    schemas['PluginUpdateInfo']['description'] = ('Catalog comparison for one installed plugin. unknown means the catalog was never synced or a version could not be compared; it never means up to date. '
                                                  'latest_version is the newest stable release (a prerelease only when the plugin already runs one); versions lists every cataloged release, newest first; can_update is true only for a managed plugin not pending removal.')
    special[('get','plugins')]=('List plugins','Installed plugins with ownership and their update status against the cached catalog (checked server-side on every read). External plugins can report update_available but never can_update.','PluginView')
    special[('post','plugins/catalog/install')]=('Install catalog plugin','Requires alias, exact catalog module and selected known version and middleware type. Duplicate checks include static files. Applies as one operation; restart handling as for plugin update.','PluginWriteResult')
    special[('get', 'crowdsec/status')] = (
        'Read CrowdSec integration status',
        'Reports engine, plugin, middleware, bouncer key, LAPI, dashboard machine, ingestion, remediation, capabilities and per-router protection separately. '
        'Plugin and middleware detection covers console-managed and external declarations under any alias. An unreadable inventory is an error, never "missing". '
        'LAPI and machine checks use role-specific registered credentials. Configured is not protected: ingestion and request blocking remain unverified without real evidence.',
        'CrowdSecStatus')
    special[('get', 'crowdsec/stats')] = (
        'Read CrowdSec statistics',
        'Aggregates alerts (detected attacks) for the window query parameter (1h, 24h, 7d or 30d; default 24h), active decisions and registered bouncers. '
        'Native machine/bouncer reads are preferred; the registered CrowdSec container remains a compatibility source. Sections that cannot be read are listed in partial. '
        'Alert countries describe alert sources, not visitors. Decisions and alerts are not request counts; visits and base-bouncer blocked requests are not fabricated.',
        'CrowdSecStats')
    schemas['UpdatePluginRequest']['required']=['version']
    schemas['CatalogInstallRequest']['required']=['alias','module_name','version']
    for path, operations in spec['paths'].items():
        short = path.removeprefix('/api/v1/')
        stem = short.split('/')[0]
        for method, op in operations.items():
            tag = 'snapshots' if stem in ('snapshots', 'operations', 'config') else stem
            tags.add(tag)
            op['tags'] = [tag]
            result = None
            if (method, short) in special:
                summary, description, result = special[(method, short)]
            else:
                kind = RESOURCE_KINDS[stem]
                result = kind + 'Resource' if method != 'delete' else kind + 'WriteResult'
                if method == 'put':
                    result = kind + 'WriteResult'
                if method == 'get':
                    summary = ('List ' + stem) if '{name}' not in path else 'Read ' + kind.lower()
                    description = 'Committed configuration with ownership, version and live observation metadata. External resources are read-only.'
                elif method == 'put':
                    summary = 'Save ' + kind.lower()
                    description = ('Create or replace the full editable payload under the path name (not PATCH). '
                                   'Send If-Match "<version>" to change an existing resource or If-None-Match: * to create one '
                                   '(428 when neither is sent, 400 when malformed or both are sent, 409 ERR_VERSION_CONFLICT when stale). '
                                   'The change is validated, rendered, published, verified and committed as one operation. '
                                   'With dry_run=true nothing is published and the preview is returned.')
                else:
                    summary = 'Delete ' + kind.lower()
                    description = ('Reference-checked deletion with If-Match "<version>". Applied as one operation by rendering the configuration without the resource. '
                                   'With dry_run=true nothing is published and the preview is returned.')
            op['summary'], op['description'] = summary, description
            if stem == 'domains' and method in ('put', 'delete'):
                op['summary'] = 'Save domain' if method == 'put' else 'Delete domain'
                op['description'] = (
                    'Requires If-Match "<version>" (If-None-Match: * to create). The change is '
                    'written to Console-owned YAML as one operation. Referenced deletion is '
                    'blocked while routers still use the domain. '
                    'Does not create DNS records or request certificates by itself.')
            if result:
                response = ref(result)
                # These list endpoints preserve a JSON array result (nil slices may be null).
                if method == 'get' and short != 'entrypoints' and (short in RESOURCE_KINDS or short in (
                        'plugins/catalog', 'crowdsec/decisions', 'crowdsec/allowlists', 'crowdsec/hub', 'snapshots/{id}/resources')):
                    response = {'type': ['array', 'null'], 'items': response}
                success = op['responses'].get('201', op['responses'].get('200'))
                success['content']['application/json']['schema'] = response
            if 'requestBody' in op:
                request_refs(op['requestBody'])
    spec['paths']['/api/v1/plugins/catalog/page']['get']['parameters'] = [
        {'name': 'q', 'in': 'query', 'schema': {'type': 'string', 'maxLength': 256}},
        {'name': 'module', 'in': 'query', 'schema': {'type': 'string', 'maxLength': 256}},
        {'name': 'limit', 'in': 'query', 'schema': {'type': 'integer', 'minimum': 1, 'maximum': 100, 'default': 25}},
        {'name': 'offset', 'in': 'query', 'schema': {'type': 'integer', 'minimum': 0, 'maximum': 1000000, 'default': 0}},
        {'name':'availability','in':'query','schema':{'type':'string','enum':['available']},'description':'Exclude configured, live and reserved modules before counting/paging. inventory_known=false disables creation when static inventory cannot be read.'},
        {'name': 'order', 'in': 'query', 'schema': {'type': 'string', 'enum': ['asc', 'desc'], 'default': 'asc'}},
    ]
    spec['tags'] = [{'name': tag} for tag in sorted(tags)]
    spec['info']['version'] = '0.11.0'
    spec['info']['description'] += (
        ' Contract scope: backend only. See docs/architecture/core-v2-frontend-integration.md. '
        'Configuration edits are direct operations: each write validates, renders, publishes, verifies '
        'and commits in one step guarded by a per-resource version (If-Match / If-None-Match). '
        'Restore points are user snapshots. The draft endpoints were removed and answer 410. Default '
        'JSON body limit 1 MiB (certificate upload 4 MiB); valid UTF-8 object, depth <=32, '
        'no duplicate or unknown struct keys. Null is rejected except below middleware.spec. '
        'Send canonical JSON names and Content-Type application/json. Runtime validates '
        'references, credentials and native configuration beyond these structural schemas. '
        'Structured errors contain code/message/operation_id/retryable; unknown routes or '
        'methods may return plain-text ServeMux errors. Listed error statuses are a shared '
        'envelope, not a promise that each operation emits every status.')
    return spec

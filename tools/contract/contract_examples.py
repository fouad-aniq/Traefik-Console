"""Synthetic documentation examples, not runtime observations or live credentials."""
from copy import deepcopy


def attach_examples(spec):
    schemas = spec['components']['schemas']
    operation = {'id': 7, 'kind': 'resource_write', 'target': 'service:app', 'status': 'succeeded',
                 'summary': 'Save service app', 'requires_restart': False, 'stage': 'committed',
                 'created_at': '2026-11-07T12:00:00Z', 'finished_at': '2026-11-07T12:00:02Z'}
    examples = {
        'UpdatePluginRequest':{'version':'v1.2.0'},
        'CatalogInstallRequest':{'alias':'demo','module_name':'github.com/traefik/plugindemo','version':'v0.2.2'},
        'CatalogRefresh':{'last_attempt_at':'','last_success_at':'','last_attempt_failed':False},
        'PluginUpdates':{'items':[],'refresh':{'last_attempt_at':'','last_success_at':'','last_attempt_failed':False}},
        'CatalogPage': {'items': [], 'total': 0, 'limit': 25, 'offset': 0, 'last_synced_at': '', 'inventory_known':True,'refresh':{'last_attempt_at':'','last_success_at':'','last_attempt_failed':False}},
        'ResolverInput': {'type': 'acme', 'environment': 'staging',
                          'email': 'operator@example.invalid', 'challenge': 'tlsalpn01'},
        'EntryPointInput': {'port': 8443, 'as_default': True, 'http3': {},
                            'http': {'tls': {'cert_resolver': 'staging'}},
                            'forwarded_headers': {'trusted_ips': ['10.0.0.0/8']}},
        'DomainInput': {'main': 'app.example.invalid', 'method': 'resolver', 'resolver': 'staging'},
        'RouterInput': {'entry_points': ['websecure'], 'domain_name': 'site', 'service': 'app'},
        'ServiceInput': {'servers': [{'url': 'http://app:8080'}], 'pass_host_header': True},
        'MiddlewareInput': {'type': 'headers', 'spec': {'customResponseHeaders': {'X-Example': 'enabled'}}},
        'ChainInput': {'middlewares': ['security@file']},
        'PluginInput': {'module_name': 'example.invalid/plugin', 'version': 'v1.0.0'},
        'CrowdSecConfigInput': {'enabled': False},
        'RestoreRequest': {'restore_token': 'example-token', 'idempotency_key': 'frontend-example-restore-001', 'skip_backup': False},
        'SnapshotCreateRequest': {'name': 'before-upgrade', 'description': 'Known-good state'},
        'SnapshotPatch': {'pinned': True},
        'DecisionInput': {'ip': '192.0.2.25', 'duration': '1h', 'reason': 'operator review'},
        'PluginSnippet': {'raw_yaml': 'experimental:\n  plugins:\n    example:\n      moduleName: example.invalid/plugin\n      version: v1.0.0\n'},
        'UploadCertificate': {'purpose': 'server', 'cert_pem': '<operator-supplied certificate PEM>',
                              'key_pem': '<operator-supplied matching private key PEM>'},
        'ApiError': {'code': 'ERR_CONFLICT', 'message': 'Configuration changed; refresh review.',
                     'operation_id': 'example-operation', 'retryable': False},
        'Operation': operation,
        'OperationPreview': {'added': [], 'changed': [{'kind': 'service', 'name': 'app'}], 'removed': [],
                             'changes': [{'id': 'service:app', 'kind': 'service', 'name': 'app', 'scope': 'dynamic', 'action': 'modified'}],
                             'static_diff': '', 'dynamic_diff': '# synthetic redacted diff', 'domain_diff': '',
                             'requires_restart': False, 'restart_reasons': [], 'env_vars_to_mount': [],
                             'validation_errors': [], 'validation_warnings': [], 'restore_token': 'example-token'},
        'SnapshotConfig': {'id': 1, 'static_yaml': 'entryPoints:\n  web:\n    address: :80\n', 'dynamic_yaml': 'http:\n  services:\n    app: {}\n', 'domains_yaml': 'tls: {}\n'},
        'Snapshot': {'id': 1, 'name': 'before-upgrade', 'description': '', 'origin': 'manual', 'pinned': False,
                     'known_good': True, 'item_count': 3, 'size_bytes': 2048, 'static_hash': '1' * 64,
                     'dynamic_hash': '2' * 64, 'domains_hash': '3' * 64, 'state_hash': '4' * 64,
                     'created_at': '2026-11-07T12:00:00Z'},
        'ConfigState': {'static_hash': '1' * 64, 'dynamic_hash': '2' * 64, 'domains_hash': '3' * 64,
                        'published_static_hash': '1' * 64, 'published_dynamic_hash': '2' * 64, 'published_domains_hash': '3' * 64,
                        'drifted': False, 'drifted_files': [], 'restart_required': False, 'running_operation': False,
                        'archived_drafts': 0, 'archived_secret_drafts': 0, 'legacy_snapshots_imported': 0},
        'ServiceResource': {'id': 'service:app', 'name': 'app', 'origin': 'managed',
                            'provider': 'file', 'status': 'active', 'read_only': False, 'version': 1,
                            'live_health': 'unknown',
                            'config': {'servers': [{'url': 'http://app:8080'}]}},
        'DomainResource': {'id': 'domain:site', 'name': 'site', 'origin': 'managed',
                           'provider': 'file', 'status': 'active', 'read_only': False, 'version': 1,
                           'live_health': 'unknown',
                           'config': {'name': 'site', 'main': 'app.example.invalid',
                                      'method': 'resolver', 'resolver': 'staging',
                                      'state': 'configured_unverified'}},
        'RouterResource': {'id': 'router:app', 'name': 'app', 'origin': 'managed',
                           'provider': 'file', 'status': 'active', 'read_only': False, 'version': 4,
                           'live_health': 'unknown',
                           'config': {'domain_name': 'site', 'entry_points': ['websecure'],
                                      'domains': ['new.example.invalid'], 'rule': 'Host(`new.example.invalid`)',
                                      'service': 'app', 'tls': {'enabled': True, 'cert_resolver': 'staging'}}},
    }
    for name, example in examples.items():
        schemas[name]['examples'] = [deepcopy(example)]
    for operations in spec['paths'].values():
        for op in operations.values():
            media = op.get('requestBody', {}).get('content', {}).get('application/json')
            if media:
                name = media['schema']['$ref'].rsplit('/', 1)[-1]
                if name in examples:
                    media['examples'] = {'synthetic': {
                        'summary': 'Illustrative only; substitute registered names and fresh hashes',
                        'description': ('PEM placeholders are not valid PEM; plugin modules and '
                                        'addresses are documentation examples, not install recommendations.'),
                        'value': deepcopy(examples[name])}}
            for status, response in op['responses'].items():
                media = response.get('content', {}).get('application/json', {})
                name = media.get('schema', {}).get('$ref', '').rsplit('/', 1)[-1]
                if name in examples and (status.startswith('2') or status == '409'):
                    media['examples'] = {'synthetic': {'summary': 'Synthetic shape example, not observed health',
                                                      'value': deepcopy(examples[name])}}
    spec['paths']['/api/v1/snapshots/{id}/restore']['post']['responses']['422']['content']['application/json']['examples'] = {
        'rolled_back': {'value': {'code': 'ERR_DEPLOY_ROLLED_BACK',
            'message': 'Apply failed at static_publication: Permission was denied for the registered operation [PERMISSION_DENIED]. Previous configuration restored. Operation 42.',
            'field_errors': {'stage': 'static_publication', 'reason_code': 'PERMISSION_DENIED', 'operation_id': '42'},
            'operation_id': 'example-operation', 'retryable': False}}}
    return spec

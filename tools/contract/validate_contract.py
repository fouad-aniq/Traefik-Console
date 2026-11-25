#!/usr/bin/env python3
"""Validate standards, regeneration, examples and optional real lab responses.

Run with the existing pinned tools/api-contract Python environment. No response
payload is written or included in validation errors (including credentials).
"""
import hashlib
import json
import pathlib
import re
from urllib.parse import urlsplit

from jsonschema import Draft202012Validator, FormatChecker

ROOT = pathlib.Path(__file__).resolve().parents[2]


class Contract:
    def __init__(self):
        self.spec = json.loads((ROOT / 'api/openapi.json').read_text())
        self.coverage = set()

    def validator(self, schema):
        return Draft202012Validator(
            {'components': self.spec['components'], **schema}, format_checker=FormatChecker())

    def check(self, schema, value, label):
        errors = list(self.validator(schema).iter_errors(value))
        if errors:
            # jsonschema's default exception prints the offending payload. Do not
            # expose request/response material, only the safe schema location.
            error = errors[0]
            location = '/'.join(map(str, error.absolute_schema_path))
            raise ValueError(f'{label}: schema validation failed at {location}')

    def validate_response(self, method, path, status, payload):
        path = urlsplit(path).path
        # Match static discovery routes before /{name}, independent of spec order.
        for template in sorted(self.spec['paths'], key=lambda p: (p.count('{'), -len(p))):
            operations = self.spec['paths'][template]
            pattern = re.sub(r'\{[^}]+\}', '[^/]+', template)
            if template.endswith('/decisions/{ip}'):
                pattern = pattern.replace('[^/]+', '.+')
            if re.fullmatch(pattern, path) and method.lower() in operations:
                operation = operations[method.lower()]
                response = operation['responses'].get(str(status))
                if response is None:
                    raise ValueError(f'Undocumented response: {method} {template} {status}')
                schema = response['content']['application/json']['schema']
                self.check(schema, payload, f'{method} {template} {status}')
                self.coverage.add((method, template, status))
                return
        raise ValueError('No documented operation for lab request')

    def report(self):
        return {'validated_operation_statuses': [
            {'method': method, 'path': path, 'status': status}
            for method, path, status in sorted(self.coverage)],
            'unique_operations': len({(method, path) for method, path, _ in self.coverage})}


def main():
    from openapi_spec_validator import validate
    from generate_contract import build, MODELS
    from contract_details import enrich, RESOURCE_KINDS
    from contract_examples import attach_examples
    from contract_auth import add_authentication

    contract = Contract()
    spec = contract.spec
    validate(spec)
    expected = json.dumps(add_authentication(attach_examples(enrich(build(), MODELS))), indent=2, ensure_ascii=False) + '\n'
    assert expected == (ROOT / 'api/openapi.json').read_text(), 'Generated contract drift'
    count = 0
    for name, schema in spec['components']['schemas'].items():
        Draft202012Validator.check_schema(schema)
        for example in schema.get('examples', []):
            contract.check(schema, example, name)
            count += 1
    operations = 0
    for path, methods in spec['paths'].items():
        for method, operation in methods.items():
            operations += 1
            assert operation.get('summary') and operation.get('description') and operation.get('tags'), path
            containers = [operation.get('requestBody', {})] + list(operation['responses'].values())
            for container in containers:
                for media in container.get('content', {}).values():
                    for example in media.get('examples', {}).values():
                        contract.check(media['schema'], example['value'], method + ' ' + path)
                        count += 1
    for kind in RESOURCE_KINDS.values():
        resource = spec['components']['schemas'][kind + 'Resource']
        for field in ('config',):
            assert resource['properties'][field] == {'$ref': '#/components/schemas/' + kind}
    # Negative checks exercise useful contract constraints, not merely generation.
    cases = [
        ('ServiceInput', {'servers': [{'url': 42}]}),
        ('ServiceInput', {'servers': [], 'unknown': True}),
        ('ResolverInput', {'type': 'tailscale'}),
        ('ResolverInput', {'type': 'acme', 'email': 'operator@example.invalid', 'challenge': 'dns01'}),
        ('DomainInput', {'main': 'example.invalid', 'method': 'files'}),
        ('SnapshotCreateRequest', {'name': 'x', 'unknown': True}),
        ('RestoreRequest', {'restore_token': 1}),
        ('ServiceResource', {'config': {'servers': []}}),
        ('AuthProviderRequest', {'kind': 'saml'}),
        ('AuthSetupRequest', {'setup_token': 'x', 'username': 'a', 'password': 'short'}),
        ('AuthLoginRequest', {'username': 'admin', 'password': 'x', 'remember': True}),
    ]
    for name, payload in cases:
        assert not contract.validator(spec['components']['schemas'][name]).is_valid(payload), name
    contract.validate_response('GET', '/api/v1/resolvers/providers', 200,
                               {'providers': [], 'custom_provider_codes': []})
    assert ('GET', '/api/v1/resolvers/providers', 200) in contract.coverage
    metadata = spec['components']['schemas']['DNSField']['properties']['is_secret']
    assert contract.validator(metadata).is_valid('[redacted]')
    assert not contract.validator(metadata).is_valid(False)
    providers = json.loads((ROOT / 'backend/internal/core/resolvers/providers/providers.json').read_text())['providers']
    for provider in providers.values():
        fields = provider['global_options'] + [field for alternative in provider['auth_alternatives']
            for group in ('required_fields', 'optional_fields') for field in alternative[group]]
        assert all((field['type'] == 'secret') == field.get('is_secret', False) for field in fields), 'Discovery secret-control workaround drift'
    provenance = json.loads((ROOT / 'api/schemas/provenance.json').read_text())
    for path, expected_hash in provenance['source_files'].items():
        assert hashlib.sha256((ROOT / path).read_bytes()).hexdigest() == expected_hash, 'Provenance drift: ' + path
    print(f'PASS: OpenAPI 3.1; deterministic generation; {operations} described operations; '
          f'{count} schema/media examples; typed resource configs; {len(cases)} negative contracts; provenance')


if __name__ == '__main__':
    main()

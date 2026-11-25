#!/usr/bin/env python3
"""Authentication part of the core-v2 OpenAPI document.

Auth routes are served by internal/api/authgate (not handlers/router.go), so
they are declared here and checked against authgate/handlers.go separately.
Every other /api/v1 route requires the tc_session cookie; state-changing
requests also require the session's X-CSRF-Token header.
"""
import copy, re

SCHEME = {'sessionCookie': {'type': 'apiKey', 'in': 'cookie', 'name': 'tc_session',
                            'description': 'HttpOnly, SameSite=Lax session cookie set by setup, login or external sign-in. '
                                           'POST/PUT/PATCH/DELETE also require the X-CSRF-Token header with the session "csrf" value.'}}

DESCRIPTION = ('Single-administrator console API. Every /api/v1 route except GET /api/v1/health and the public '
               '/api/v1/auth endpoints requires the tc_session cookie; state-changing requests also require the '
               'X-CSRF-Token header. Host/origin checks and write preconditions remain separate safeguards.')

GO = {  # component name -> Go type whose JSON fields the checker compares
    'AuthStatus': 'internal/api/authgate.statusView',
    'AuthSession': 'internal/api/authgate.sessionView',
    'AuthUser': 'internal/api/authgate.userView',
    'AuthSetupRequest': 'internal/api/authgate.setupRequest',
    'AuthLoginRequest': 'internal/api/authgate.loginRequest',
    'AuthPasswordRequest': 'internal/api/authgate.passwordRequest',
    'AuthProviderRequest': 'internal/api/authgate.providerRequest',
    'AuthProviderDeleteRequest': 'internal/api/authgate.deleteProviderRequest',
    'AuthProviderList': 'internal/api/authgate.providerList',
    'AuthProviderSettings': 'internal/auth.ProviderSettings',
    'AuthPublicProvider': 'internal/auth.PublicProvider',
    'AuthSessionInfo': 'internal/auth.SessionInfo',
    'AuthEvent': 'internal/auth.Event',
}

S, B, I = {'type': 'string'}, {'type': 'boolean'}, {'type': 'integer'}
T = {'type': 'string', 'format': 'date-time'}
SA = {'type': 'array', 'items': S}
def ref(n): return {'$ref': '#/components/schemas/' + n}

PROPS = {
    'AuthStatus': {'setup_required': B, 'authenticated': B, 'session': ref('AuthSession'), 'providers': {'type': 'array', 'items': ref('AuthPublicProvider'), 'description': 'One sign-in button per configured provider.'}},
    'AuthSession': {'user': ref('AuthUser'), 'csrf': {'type': 'string', 'description': 'Send as X-CSRF-Token on state-changing requests. Kept in memory only.'}, 'expires_at': T},
    'AuthUser': {'username': S, 'method': {'type': 'string', 'enum': ['password', 'oauth']}, 'provider': {'type': 'string', 'description': 'Provider label for oauth sessions.'}},
    'AuthSetupRequest': {'setup_token': {'type': 'string', 'description': 'One-time token printed in the server log at start-up (or by `server admin setup-token`).'},
                         'username': {'type': 'string', 'pattern': '^[A-Za-z0-9._@-]{3,64}$'}, 'password': {'type': 'string', 'minLength': 12, 'maxLength': 128, 'writeOnly': True}},
    'AuthLoginRequest': {'username': S, 'password': {'type': 'string', 'writeOnly': True}},
    'AuthPasswordRequest': {'current_password': {'type': 'string', 'writeOnly': True}, 'new_password': {'type': 'string', 'minLength': 12, 'maxLength': 128, 'writeOnly': True}},
    'AuthProviderRequest': {'current_password': {'type': 'string', 'writeOnly': True}, 'kind': {'type': 'string', 'enum': ['oidc', 'oauth2']},
                            'display_name': S, 'issuer': S, 'authorize_url': S, 'token_url': S, 'userinfo_url': S, 'client_id': S,
                            'client_secret': {'type': 'string', 'writeOnly': True, 'description': 'Empty keeps the stored secret.'},
                            'clear_client_secret': B, 'scopes': SA, 'identity_claim': S, 'allowed_identities': SA, 'redirect_url': S},
    'AuthProviderDeleteRequest': {'current_password': {'type': 'string', 'writeOnly': True}},
    'AuthProviderList': {'items': {'type': 'array', 'items': ref('AuthProviderSettings')}},
    'AuthProviderSettings': {'id': {'type': 'string', 'pattern': '^[a-z0-9][a-z0-9-]{0,39}$'}, 'kind': {'type': 'string', 'enum': ['oidc', 'oauth2']}, 'display_name': S, 'issuer': S,
                             'authorize_url': S, 'token_url': S, 'userinfo_url': S, 'client_id': S, 'scopes': SA, 'identity_claim': S,
                             'allowed_identities': SA, 'redirect_url': S, 'credential_stored': B, 'updated_at': T},
    'AuthPublicProvider': {'id': S, 'display_name': S},
    'AuthSessionInfo': {'id': S, 'method': S, 'provider': S, 'ip': S, 'user_agent': S, 'signed_in_at': T, 'last_seen_at': T, 'current': B},
    'AuthEvent': {'at': T, 'event': S, 'outcome': S, 'ip': S, 'detail': S},
}

# (method, path, summary, request schema, response schema, public, extra statuses)
ROUTES = [
    ('get', '/api/v1/auth/status', 'Sign-in state for the SPA bootstrap', None, 'AuthStatus', True, []),
    ('post', '/api/v1/auth/setup', 'Create the administrator with the one-time setup token (only while none exists)', 'AuthSetupRequest', 'AuthSession', True, ['401', '409', '415', '422', '429']),
    ('post', '/api/v1/auth/login', 'Sign in with username and password', 'AuthLoginRequest', 'AuthSession', True, ['401', '409', '415', '422', '429']),
    ('get', '/api/v1/auth/oauth/start', 'Begin external sign-in with ?provider=<id> (browser navigation; 302 to the provider)', None, None, True, ['302']),
    ('get', '/api/v1/auth/oauth/callback', 'Provider redirect target (302 to the SPA)', None, None, True, ['302']),
    ('post', '/api/v1/auth/logout', 'End the current session', None, None, False, ['401', '403']),
    ('post', '/api/v1/auth/password', 'Change the password; ends every other session', 'AuthPasswordRequest', 'AuthSession', False, ['401', '403', '422', '429']),
    ('get', '/api/v1/auth/sessions', 'Active sessions', None, 'AuthSessionList', False, ['401']),
    ('post', '/api/v1/auth/sessions/revoke-others', 'Sign out every other session', None, None, False, ['401', '403']),
    ('post', '/api/v1/auth/sessions/{id}/revoke', 'Sign out one other session', None, None, False, ['401', '403', '404', '409']),
    ('get', '/api/v1/auth/activity', 'Recent security events', None, 'AuthEventList', False, ['401']),
    ('get', '/api/v1/auth/providers', 'Sign-in providers (client secrets never returned)', None, 'AuthProviderList', False, ['401']),
    ('post', '/api/v1/auth/providers', 'Add a sign-in provider (re-enter current password); 201', 'AuthProviderRequest', 'AuthProviderSettings', False, ['201', '401', '403', '409', '422', '429', '502']),
    ('put', '/api/v1/auth/providers/{id}', 'Replace a sign-in provider (re-enter current password)', 'AuthProviderRequest', 'AuthProviderSettings', False, ['401', '403', '404', '422', '429', '502']),
    ('post', '/api/v1/auth/providers/{id}/delete', 'Remove a sign-in provider and end its sessions (re-enter current password)', 'AuthProviderDeleteRequest', None, False, ['401', '403', '404', '422', '429']),
]


def add_authentication(spec):
    spec = copy.deepcopy(spec)
    spec.pop('x-authentication-deferred', None)
    spec['info']['title'] = 'Traefik Console core-v2 API'
    spec['info']['description'] = DESCRIPTION
    spec['security'] = [{'sessionCookie': []}]
    spec['components']['securitySchemes'] = SCHEME
    schemas = spec['components']['schemas']
    for name, props in PROPS.items():
        schemas[name] = {'type': 'object', 'x-go-type': GO[name], 'additionalProperties': False, 'properties': props}
    schemas['AuthSessionList'] = {'type': 'object', 'required': ['items'], 'additionalProperties': False, 'properties': {'items': {'type': 'array', 'items': ref('AuthSessionInfo')}}}
    schemas['AuthEventList'] = {'type': 'object', 'required': ['items'], 'additionalProperties': False, 'properties': {'items': {'type': 'array', 'items': ref('AuthEvent')}}}

    health = spec['paths']['/api/v1/health']['get']
    health['security'] = []
    health['responses']['200']['content']['application/json']['schema']['properties']['authentication'] = {'const': 'session'}
    health['description'] = 'Manager process liveness. Public; not Traefik traffic health.'

    csrf = {'name': 'X-CSRF-Token', 'in': 'header', 'required': True, 'description': 'The "csrf" value of the current session.', 'schema': {'type': 'string'}}
    for path, ops in spec['paths'].items():
        if path == '/api/v1/health':
            continue
        for method, op in ops.items():
            op['responses'].setdefault('401', {'description': 'Sign-in required', 'content': {'application/json': {'schema': ref('ApiError')}}})
            if method in ('post', 'put', 'patch', 'delete'):
                op.setdefault('parameters', []).append(csrf)

    for method, path, summary, body, result, public, statuses in ROUTES:
        op = {'operationId': method + '_' + re.sub(r'[^A-Za-z0-9]+', '_', path).strip('_'), 'tags': ['authentication'], 'summary': summary,
              'description': summary + ('. Public.' if public else '. Requires the session cookie.'), 'responses': {}}
        if result:
            op['responses']['200'] = {'description': 'Successful result', 'content': {'application/json': {'schema': ref(result)}}}
        elif '302' not in statuses:
            op['responses']['200'] = {'description': 'Successful result', 'content': {'application/json': {'schema': {'type': 'object'}}}}
        for status in ['400', '500', '503'] + [x for x in statuses if x != '201']:
            op['responses'][status] = ({'description': 'Redirect'} if status == '302'
                                       else {'description': 'Structured API error', 'content': {'application/json': {'schema': ref('ApiError')}}})
        if '201' in statuses and result:
            op['responses']['201'] = op['responses'].pop('200')
            op['responses']['201']['description'] = 'Created'
        params = [{'name': n, 'in': 'path', 'required': True, 'schema': {'type': 'string'}} for n in re.findall(r'\{([^}]+)\}', path)]
        if path == '/api/v1/auth/oauth/start':
            params.append({'name': 'provider', 'in': 'query', 'required': True, 'schema': {'type': 'string'}})
        if params:
            op['parameters'] = params
        if body:
            op['requestBody'] = {'required': True, 'content': {'application/json': {'schema': ref(body)}}}
        if public:
            op['security'] = []
        elif method != 'get':
            op.setdefault('parameters', []).append(csrf)
        spec['paths'].setdefault(path, {})[method] = op
    tags = spec.setdefault('tags', [])
    if not any(t.get('name') == 'authentication' for t in tags):
        tags.append({'name': 'authentication', 'description': 'Single administrator: first-run setup, password sign-in, any number of external (OIDC/OAuth 2.0) providers, sessions.'})
    return spec


def check_authentication(spec, root):
    """Auth routes in authgate/handlers.go must equal the declared ones."""
    source = (root / 'backend/internal/api/authgate/handlers.go').read_text()
    served = {(m.lower(), p) for m, p in re.findall(r'g\.handle\("(GET|POST|PUT|PATCH|DELETE) ([^"]+)"', source)}
    declared = {(m, p) for m, p, *_ in ROUTES}
    if served != declared:
        raise SystemExit('auth route drift: ' + str(sorted(served ^ declared)))
    if spec.get('security') != [{'sessionCookie': []}] or 'x-authentication-deferred' in spec:
        raise SystemExit('session authentication must be the default security requirement')
    if spec['paths']['/api/v1/health']['get'].get('security') != []:
        raise SystemExit('health must stay public')
    return len(served)

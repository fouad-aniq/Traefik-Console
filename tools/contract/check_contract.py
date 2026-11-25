#!/usr/bin/env python3
"""Check the scoped API route/DTO baselines without touching legacy contracts."""
import json,pathlib,re,subprocess,os,sys
from generate_contract import build, MODELS
from contract_details import enrich
from contract_examples import attach_examples
from contract_auth import add_authentication, check_authentication
ROOT=pathlib.Path(__file__).resolve().parents[2]
def main():
    spec=json.loads((ROOT/'api/openapi.json').read_text())
    if spec!=add_authentication(attach_examples(enrich(build(),MODELS))):raise SystemExit('generated OpenAPI drift; regenerate and review deliberately')
    router=(ROOT/'backend/internal/api/handlers/router.go').read_text()
    # handleDraftGone routes are the 410 tombstones for the removed draft protocol; they are not part of the contract.
    routes={(m.lower(),p.replace('{ip...}','{ip}')) for m,p,h in re.findall(r'HandleFunc\("(GET|POST|PUT|PATCH|DELETE) ([^"]+)", ([^\n]+)\)',router) if 'handleDraftGone' not in h}
    declared={(m,p) for p,ops in spec['paths'].items() for m in ops if m in {'get','post','put','patch','delete'} and not p.startswith('/api/v1/auth/')}
    if routes!=declared:raise SystemExit('API route drift: '+str(sorted(routes^declared)))
    raw=subprocess.check_output(['go','run',str(ROOT/'tools/contract/inspect_models.go'),str(ROOT/'backend')],cwd=ROOT,env=dict(os.environ,GOTOOLCHAIN='local',GOPROXY='off'))
    baseline=json.loads((ROOT/'api/schemas/models.json').read_text());actual=json.loads(raw)
    if actual!=baseline:raise SystemExit('public DTO baseline drift; regenerate and review deliberately')
    for name,schema in spec['components']['schemas'].items():
        model=schema.get('x-go-type')
        if model and set(schema.get('properties',{}))!=set(actual[model]):raise SystemExit('OpenAPI DTO field drift: '+name)
    providers=json.loads((ROOT/'backend/internal/core/resolvers/providers/providers.json').read_text())['providers']
    if set(providers)!={'cloudflare','ovh','duckdns','namecheap','gcloud','azuredns','hetzner','digitalocean'}:raise SystemExit('guided DNS scope drift')
    auth_routes=check_authentication(spec,ROOT)
    deps=subprocess.check_output(['go','list','-buildvcs=false','-deps','./cmd/server'],cwd=ROOT/'backend',env=dict(os.environ,GOTOOLCHAIN='local',GOPROXY='off')).decode().splitlines()
    if 'testing' in deps:raise SystemExit('production server must not import test support')
    if 'github.com/traefik-console/internal/core' in deps or list((ROOT/'backend/internal/core').glob('*.go')):raise SystemExit('root core aggregation must not be reintroduced; compose resource packages directly')
    if any('/internal/broker' in x for x in deps) or (ROOT/'backend/cmd/broker').exists():raise SystemExit('custom broker process/transport must be absent')
    if 'github.com/traefik-console/internal/docker' not in deps:raise SystemExit('server must use the restricted Docker handler directly')
    for path in (ROOT/'backend').rglob('*.go'):
        if '/internal/docker/' in str(path):continue
        raw=path.read_text()
        if 'ExecCrowdSecCLI' in raw or 'FetchTraefikLogs' in raw:raise SystemExit('raw container command/log capability leaked: '+str(path))
    print(f'PASS: {len(routes)} routes + {auth_routes} auth routes; {len(actual)} DTO shapes; eight guided DNS providers; session authentication by default; direct restricted Docker handler; no custom broker process')
if __name__=='__main__':main()

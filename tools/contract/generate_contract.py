#!/usr/bin/env python3
"""Rebuild the scoped OpenAPI document from reviewed route bindings and DTO baseline."""
import json,pathlib,re
from contract_details import enrich
from contract_examples import attach_examples
from contract_auth import add_authentication
ROOT=pathlib.Path(__file__).resolve().parents[2]
MODELS=json.loads((ROOT/'api/schemas/models.json').read_text())
SELECT={
'CrowdSecStatus':'internal/core/crowdsec.CrowdSecStatus','CrowdSecStats':'internal/core/crowdsec.CrowdSecStats','CrowdSecMachineStatus':'internal/core/crowdsec.MachineStatus','CrowdSecObservationStatus':'internal/core/crowdsec.ObservationStatus','CrowdSecMetricsSnapshot':'internal/core/crowdsec.MetricsSnapshot','CrowdSecHubItem':'internal/docker.HubItem','CrowdSecCapabilities':'internal/core/crowdsec.CrowdSecCapabilities','CrowdSecSources':'internal/core/crowdsec.CrowdSecSources','CrowdSecAlertPage':'internal/core/crowdsec.AlertPage','CrowdSecAlert':'internal/docker.Alert','CrowdSecAllowlist':'internal/docker.Allowlist','CrowdSecAllowlistEntry':'internal/docker.AllowlistEntry','CrowdSecPluginStatus':'internal/core/crowdsec.PluginStatus','CrowdSecMiddlewareStatus':'internal/core/crowdsec.MiddlewareStatus','CrowdSecKeyStatus':'internal/core/crowdsec.KeyStatus','CrowdSecLAPIStatus':'internal/core/crowdsec.LAPIStatus','CrowdSecEngineStatus':'internal/core/crowdsec.EngineStatus','CrowdSecRouterProtection':'internal/core/crowdsec.RouterProtection','CrowdSecCountItem':'internal/core/crowdsec.CountItem','CrowdSecTimelinePoint':'internal/core/crowdsec.TimelinePoint','CrowdSecBouncer':'internal/docker.Bouncer', 'CrowdSecConnectionSettings':'internal/core/crowdsec.ConnectionSettings','CrowdSecConnectionTest':'internal/core/crowdsec.ConnectionTest','CrowdSecProtectionSettings':'internal/core/crowdsec.ProtectionSettings','CrowdSecEnvVariable':'internal/core/crowdsec.EnvVariable','CrowdSecRuntimeRegistration':'internal/core/crowdsec.RuntimeRegistration', 'CatalogRefresh':'internal/models.CatalogRefresh','PluginUpdateInfo':'internal/models.PluginUpdateInfo','PluginUpdate':'internal/models.PluginUpdate','PluginUpdates':'internal/models.PluginUpdates','UpdatePluginRequest':'internal/core/plugins.UpdatePluginRequest','CatalogInstallRequest':'internal/core/plugins.CatalogInstallRequest',
 'CatalogPage':'internal/models.CatalogPageView','ApiError':'internal/api/response.Error','Operation':'internal/models.OperationRow','OperationPreview':'internal/models.OperationPreview','OperationResult':'internal/models.OperationResult','RestoreRequest':'internal/models.RestoreRequest','RestoreResult':'internal/models.RestoreResult','ConfigState':'internal/models.ConfigState','ConsoleUpdate':'internal/models.ConsoleUpdate','SnapshotCreateRequest':'internal/models.SnapshotCreateRequest','SnapshotPatch':'internal/models.SnapshotPatch','SnapshotResource':'internal/models.SnapshotResource','ItemChange':'internal/models.ItemChange','EnvMountInstruction':'internal/models.EnvMountInstruction','ValidationError':'internal/models.ValidationError','Snapshot':'internal/models.SnapshotRow','SnapshotConfig':'internal/models.SnapshotConfig','Resolver':'internal/models.ResolverInput','EntryPoint':'internal/models.EntryPointConfig','EntryPointForwardedHeaders':'internal/models.EntryPointForwardedHeaders','EntryPointProxyProtocol':'internal/models.EntryPointProxyProtocol','EntryPointTransport':'internal/models.EntryPointTransport','EntryPointHTTP':'internal/models.EntryPointHTTP','EntryPointRedirect':'internal/models.EntryPointRedirect','EntryPointTLS':'internal/models.EntryPointTLS','EntryPointTLSDomain':'internal/models.EntryPointTLSDomain','EntryPointHTTP2':'internal/models.EntryPointHTTP2','EntryPointHTTP3':'internal/models.EntryPointHTTP3','EntryPointUDP':'internal/models.EntryPointUDP','EntryPointObservability':'internal/models.EntryPointObservability','EntryPointUsage':'internal/models.EntryPointUsage','Propagation':'internal/models.PropagationConfig','Domain':'internal/models.DomainConfig','Certificate':'internal/models.CertificateConfig','Router':'internal/models.RouterConfig','RouterTLS':'internal/models.RouterTLS','TLSDomain':'internal/models.TLSDomain','Service':'internal/models.ServiceConfig','ServerEndpoint':'internal/models.ServerEndpoint','ServiceHealth':'internal/models.ServiceHealth','Middleware':'internal/models.MiddlewareConfig','Chain':'internal/models.ChainConfig','Plugin':'internal/models.StaticPluginConfig','CrowdSecConfig':'internal/core/crowdsec.CrowdSecConfig','CrowdSecDecision':'internal/docker.Decision','DomainProbe':'internal/models.DomainProbeResult','Overview':'internal/traefik/status.OverviewObservation','ACMEInventory':'internal/traefik/status.ACMEInventory','ACMEStorageObservation':'internal/traefik/status.ACMEStorageObservation','CertificateInfo':'internal/traefik/status.CertificateInfo','ContainerStatus':'internal/docker.ContainerStatus','UIResource':'internal/models.UIResource','ServiceDiagnostic':'internal/traefik/status.ServiceDiagnostic','ServiceRouterObservation':'internal/traefik/status.ServiceRouterObservation','DNSProvider':'internal/core/resolvers/providers.Provider','DNSField':'internal/core/resolvers/providers.Field','DNSAlternative':'internal/core/resolvers/providers.Alternative','CatalogPlugin':'internal/models.CatalogPluginRow','CertificateUsage':'internal/models.CertificateUsage','DomainUsage':'internal/models.DomainUsage','DomainRef':'internal/models.DomainRef','RouterRef':'internal/models.RouterRef','CertificateRef':'internal/models.CertificateRef','MiddlewareUsage':'internal/models.MiddlewareUsage','ChainRef':'internal/models.ChainRef'}
NAMES={path.rsplit('.',1)[1]:name for name,path in SELECT.items()}
NAMES['CatalogPluginView']='CatalogPlugin'
def ref(name):return {'$ref':'#/components/schemas/'+name}
def shape(go):
 go=go.lstrip('*')
 if go.startswith('[]'):return {'type':'array','items':shape(go[2:])}
 if go.startswith('map[string]'):return {'type':'object','additionalProperties':shape(go[11:])}
 if go in ('any','T','json.RawMessage'):return {}
 if go=='string':return {'type':'string'}
 if go=='bool':return {'type':'boolean'}
 if go.startswith(('int','uint')):return {'type':'integer'}
 if go.startswith('float'):return {'type':'number'}
 if go=='time.Time':return {'type':'string','format':'date-time'}
 if go.endswith(('ItemKind','OperationKind')):return {'type':'string'}
 if go.endswith('ItemScope'):return {'type':'string','enum':['static','dynamic']}
 if go.endswith('ItemStatus'):return {'type':'string','enum':['active']}
 name=NAMES.get(go.rsplit('.',1)[-1]);return ref(name) if name else {}
def build():
 schemas={}
 for name,path in SELECT.items():
  fields=MODELS[path];schema={'type':'object','x-go-type':path,'properties':{k:shape(v['type']) for k,v in fields.items()},'additionalProperties':False}
  schemas[name]=schema
 schemas['CrowdSecConfig']['properties']['lapi_key']['description']='Write-only credential input; reads return the retain marker ••••••••.'
 schemas['CrowdSecConfig']['properties']['enrollment_ids']['items'].update(minimum=1,maximum=9007199254740991)
 schemas['Resolver']['properties']['type'].update(enum=['acme','custom'])
 schemas['Resolver']['description']='Exactly eight guided DNS providers. Custom YAML has bounded native ACME validation and external credentials; source-audited does not mean live verified.'
 schemas['Middleware']['properties']['spec']['description']='Native HTTP fields use the committed Traefik v3.7.14 recursive inventory. Third-party plugin settings are opaque; [redacted] retains an existing leaf under the resource version.'
 schemas['Snapshot']['properties']['origin'].update(enum=['manual','initial','pre_restore','pre_static','scheduled','legacy_import','adopted'])
 schemas['Snapshot']['description']='Restore point metadata. The sealed state and the exact managed file bytes are stored; no YAML or secrets are returned here. Pinned snapshots are never pruned; known_good marks a verified-working configuration.'
 schemas['Operation']['properties']['status'].update(enum=['running','succeeded','restart_required','rolled_back','abandoned','restoring','cleanup_pending','recovery_required'])
 schemas['Operation']['description']='One entry of the operations log. recovery_required blocks further writes until the operation is recovered or abandoned. restart_required means the change is saved and Traefik must be restarted manually.'
 schemas['ConfigState']['description']='Drift, restart and recovery state of the three managed files. drifted means a file changed outside the console since the last publish; adopt accepts the on-disk files.'
 schemas['RestoreRequest']['required']=['restore_token','idempotency_key']
 schemas['RestoreRequest']['properties']['idempotency_key'].update(minLength=16,maxLength=128,pattern='^[A-Za-z0-9_-]+$')
 schemas['SnapshotCreateRequest']['required']=['name']
 schemas['SnapshotCreateRequest']['properties']['name'].update(minLength=1,maxLength=100)
 for kind in ('Resolver','Domain','Certificate','Router','Service','Middleware','Chain','Plugin','CrowdSecConfig'):
  pass
 schemas['SnapshotPage']={'type':'object','required':['items','next_cursor'],'additionalProperties':False,'properties':{'items':{'type':'array','items':ref('Snapshot')},'next_cursor':{'type':'integer','minimum':0,'description':'0 when there is no further page; otherwise pass as cursor.'}}}
 schemas['OperationPage']={'type':'object','required':['items','next_cursor'],'additionalProperties':False,'properties':{'items':{'type':'array','items':ref('Operation')},'next_cursor':{'type':'integer','minimum':0}}}
 for kind in ('Resolver','EntryPoint','Domain','Certificate','Router','Service','Middleware','Chain','Plugin'):
  schemas[kind+'WriteResult']={'type':'object','additionalProperties':False,'required':['restart_required'],'properties':{'operation':ref('Operation'),'preview':ref('OperationPreview'),'resource':ref(kind+'Resource'),'restart_required':{'type':'boolean'},'warnings':{'type':'array','items':ref('ValidationError')}}}
 schemas['CrowdSecConfigWriteResult']={'type':'object','additionalProperties':False,'required':['restart_required'],'properties':{'operation':ref('Operation'),'preview':ref('OperationPreview'),'resource':ref('CrowdSecConfig'),'restart_required':{'type':'boolean'},'warnings':{'type':'array','items':ref('ValidationError')}}}
 schemas['UploadCertificate']={'type':'object','required':['cert_pem'],'additionalProperties':False,'properties':{'purpose':{'type':'string','enum':['server','ca']},'cert_pem':{'type':'string','maxLength':1048576},'key_pem':{'type':'string','maxLength':1048576,'writeOnly':True}}}
 schemas['DecisionInput']={'type':'object','required':['ip'],'additionalProperties':False,'properties':{'ip':{'type':'string'},'duration':{'type':'string','description':'Positive Go duration, at most seven days.'},'reason':{'type':'string','maxLength':256}}}
 schemas['AllowlistInput']={'type':'object','required':['name','description'],'additionalProperties':False,'properties':{'name':{'type':'string','pattern':'^traefik-console-[a-z][a-z0-9_-]{0,47}$'},'description':{'type':'string','minLength':1,'maxLength':512}}}
 schemas['AllowlistEntryInput']={'type':'object','required':['value'],'additionalProperties':False,'properties':{'value':{'type':'string','description':'One IP address or CIDR.'},'description':{'type':'string','maxLength':512},'expiration':{'type':'string','description':'Optional positive Go duration, at most one year.'}}}
 schemas['PluginSnippet']={'type':'object','required':['raw_yaml'],'additionalProperties':False,'properties':{'raw_yaml':{'type':'string'},'short_name':{'type':'string'},'middleware_name':{'type':'string'}}}
 def resource(kind):return ref(kind+'Resource')
 bindings={}
 def put(method,path,body=None,result=None,array=False):bindings[(method,path)]=(body,result,array)
 for name,result in [('crowdsec/status','CrowdSecStatus'),('crowdsec/stats','CrowdSecStats'),('crowdsec/metrics','CrowdSecMetricsSnapshot'),('overview','Overview'),('traefik/container','ContainerStatus'),('acme/certificates','ACMEInventory'),('crowdsec/config','CrowdSecConfig')]:put('get','/api/v1/'+name,result=result)
 for stem,kind in [('resolvers','Resolver'),('domains','Domain'),('certificates','Certificate'),('routers','Router'),('services','Service'),('middlewares','Middleware'),('chains','Chain'),('plugins','Plugin')]:
  put('get','/api/v1/'+stem,result=kind,array=True)
  put('put','/api/v1/'+stem+'/{name}',body='UploadCertificate' if stem=='certificates' else kind,result=kind)
  put('delete','/api/v1/'+stem+'/{name}')
 for stem,kind in [('resolvers','Resolver'),('routers','Router'),('services','Service'),('entrypoints','EntryPoint')]:put('get','/api/v1/'+stem+'/{name}',result=kind)
 put('put','/api/v1/entrypoints/{name}',body='EntryPoint',result='EntryPoint');put('delete','/api/v1/entrypoints/{name}')
 put('get','/api/v1/snapshots',result='SnapshotPage');put('post','/api/v1/snapshots','SnapshotCreateRequest','Snapshot')
 put('get','/api/v1/snapshots/{id}',result='Snapshot');put('patch','/api/v1/snapshots/{id}','SnapshotPatch','Snapshot');put('delete','/api/v1/snapshots/{id}')
 put('get','/api/v1/snapshots/{id}/config',result='SnapshotConfig');put('get','/api/v1/snapshots/{id}/resources',result='SnapshotResource',array=True)
 put('get','/api/v1/snapshots/{id}/restore-preview',result='OperationPreview');put('post','/api/v1/snapshots/{id}/restore','RestoreRequest','RestoreResult')
 put('get','/api/v1/snapshots/compare/{from}/{to}',result='OperationPreview')
 put('get','/api/v1/operations',result='OperationPage');put('get','/api/v1/operations/{id}',result='Operation');put('post','/api/v1/operations/{id}/recover',result='Operation');put('post','/api/v1/operations/{id}/abandon',result='Operation')
 put('get','/api/v1/config/state',result='ConfigState');put('post','/api/v1/config/adopt',result='Operation')
 put('post','/api/v1/domains/{name}/probe',result='DomainProbe');put('get','/api/v1/services/{name}/diagnostics',result='ServiceDiagnostic')
 put('put','/api/v1/crowdsec/connection','CrowdSecConnectionSettings','CrowdSecConfigWriteResult');put('post','/api/v1/crowdsec/connection/test','CrowdSecConnectionSettings','CrowdSecConnectionTest')
 put('put','/api/v1/crowdsec/config','CrowdSecConfig','CrowdSecConfigWriteResult');put('get','/api/v1/crowdsec/decisions',result='CrowdSecDecision',array=True);put('post','/api/v1/crowdsec/decisions','DecisionInput');put('delete','/api/v1/crowdsec/decisions/{ip}')
 put('delete','/api/v1/crowdsec/decisions/id/{id}');put('get','/api/v1/crowdsec/alerts',result='CrowdSecAlertPage');put('get','/api/v1/crowdsec/alerts/{id}',result='CrowdSecAlert');put('get','/api/v1/crowdsec/metrics',result='CrowdSecMetricsSnapshot');put('get','/api/v1/crowdsec/hub',result='CrowdSecHubItem',array=True)
 put('get','/api/v1/crowdsec/allowlists',result='CrowdSecAllowlist',array=True);put('post','/api/v1/crowdsec/allowlists','AllowlistInput');put('post','/api/v1/crowdsec/allowlists/check','AllowlistEntryInput');put('post','/api/v1/crowdsec/allowlists/{name}/entries','AllowlistEntryInput');put('delete','/api/v1/crowdsec/allowlists/{name}/entries','AllowlistEntryInput');put('delete','/api/v1/crowdsec/allowlists/{name}')
 put('get','/api/v1/plugins/catalog/page',result='CatalogPage');put('get','/api/v1/plugins/catalog',result='CatalogPlugin',array=True);put('post','/api/v1/plugins/catalog/sync');put('post','/api/v1/plugins/yaml','PluginSnippet','Plugin')
 put('post','/api/v1/plugins/{name}/update',body='UpdatePluginRequest',result='Plugin');put('post','/api/v1/plugins/catalog/install',body='CatalogInstallRequest',result='Plugin')
 for method,path in [('get','/api/v1/health'),('get','/api/v1/entrypoints'),('get','/api/v1/traefik/logs'),('post','/api/v1/traefik/restart'),('get','/api/v1/resolvers/providers')]:put(method,path)
 paths={};resource_types={'Resolver','EntryPoint','Domain','Certificate','Router','Service','Middleware','Chain','Plugin'}
 for (method,path),(body,result,array) in sorted(bindings.items()):
  response=resource(result) if result in resource_types else ref(result) if result else {'type':'object'}
  if array:response={'type':'array','items':response}
  op={'operationId':method+'_'+re.sub(r'[^A-Za-z0-9]+','_',path).strip('_'),'responses':{'200':{'description':'Successful result','content':{'application/json':{'schema':response}}}}}
  if method=='post' and path in ('/api/v1/snapshots',) or method=='post' and (path.endswith('/crowdsec/decisions') or path.endswith('/crowdsec/allowlists') or path.endswith('/entries')):op['responses']['201']=op['responses'].pop('200')
  for status in ['400','403','404','409','413','422','423','428','500','502','503','507']:op['responses'][status]={'description':'Structured API error','content':{'application/json':{'schema':ref('ApiError')}}}
  params=[]
  for name in re.findall(r'\{([^}]+)\}',path):params.append({'name':name,'in':'path','required':True,'schema':{'type':'integer','minimum':1} if name in ('id','from','to') else {'type':'string'}})
  versioned=method in ('put','delete') and '/api/v1/crowdsec/decisions' not in path and '/api/v1/crowdsec/allowlists' not in path and not path.startswith('/api/v1/snapshots') or method=='post' and path=='/api/v1/plugins/{name}/update'
  writes=versioned or method=='post' and path in ('/api/v1/plugins/yaml','/api/v1/plugins/catalog/install')
  if versioned:
   params.append({'name':'If-Match','in':'header','required':False,'description':'Current resource version as a quoted integer, for example "3". Required to change or delete an existing resource unless If-None-Match: * is sent. Mutually exclusive with If-None-Match.','schema':{'type':'string','pattern':'^"?[1-9][0-9]*"?$'}})
   if method=='put':params.append({'name':'If-None-Match','in':'header','required':False,'description':'Send * to create a resource that must not exist yet (expected version 0). Mutually exclusive with If-Match.','schema':{'type':'string','const':'*'}})
  if writes:
   params.append({'name':'Idempotency-Key','in':'header','required':False,'description':'Optional 16-128 character key [A-Za-z0-9_-]. Repeating a completed write with the same key returns the recorded result instead of applying it twice.','schema':{'type':'string','minLength':16,'maxLength':128,'pattern':'^[A-Za-z0-9_-]+$'}})
   params.append({'name':'dry_run','in':'query','required':False,'description':'true validates and renders the change without publishing it and returns the preview (HTTP 200, no operation).','schema':{'type':'boolean','default':False}})
   op['responses']['202']={'description':'Saved, but Traefik could not be restarted. Body is the write result plus error ERR_MANUAL_RESTART_REQUIRED; restart Traefik manually.','content':{'application/json':{'schema':response}}}
  if method=='delete' and path=='/api/v1/snapshots/{id}':params.append({'name':'force','in':'query','required':False,'description':'true deletes a pinned snapshot.','schema':{'type':'boolean','default':False}})
  if method=='get' and path in ('/api/v1/snapshots','/api/v1/operations'):
   params.append({'name':'limit','in':'query','required':False,'schema':{'type':'integer','minimum':1,'maximum':100,'default':25}})
   params.append({'name':'cursor','in':'query','required':False,'description':'next_cursor from the previous page.','schema':{'type':'integer','minimum':0}})
  if method=='post' and path=='/api/v1/snapshots/{id}/restore':
   params.append({'name':'Idempotency-Key','in':'header','required':False,'description':'Optional; the body idempotency_key is the authoritative key.','schema':{'type':'string'}})
  if path.endswith('/catalog') and method=='get':params.append({'name':'q','in':'query','schema':{'type':'string','maxLength':256}})
  if params:op['parameters']=params
  if body:op['requestBody']={'required':True,'content':{'application/json':{'schema':ref(body)}}}
  paths.setdefault(path,{})[method]=op
 paths['/api/v1/health']['get']['responses']['200']['content']['application/json']['schema']={'type':'object','required':['status','authentication'],'properties':{'status':{'const':'ok'},'authentication':{'const':'deferred'}}}
 paths['/api/v1/traefik/logs']['get']['description']='Returns a restriction notice. Raw container logs are not exposed.'
 paths['/api/v1/resolvers/providers']['get']['responses']['200']['content']['application/json']['schema']={'type':'object','properties':{'providers':{'type':'array','items':ref('DNSProvider')},'custom_provider_codes':{'type':'array','items':{'type':'string'}}}}
 return {'openapi':'3.1.0','info':{'title':'Traefik Console core-v2 development API','version':'0.2.0','description':'Authentication is explicitly deferred by the user. Loopback binding, host/origin checks and write preconditions are separate safeguards; this is not a production authentication boundary.'},'servers':[{'url':'http://127.0.0.1:8081'}],'security':[],'x-authentication-deferred':True,'paths':paths,'components':{'schemas':schemas}}
if __name__=='__main__':(ROOT/'api/openapi.json').write_text(json.dumps(add_authentication(attach_examples(enrich(build(),MODELS))),indent=2,ensure_ascii=False)+'\n')

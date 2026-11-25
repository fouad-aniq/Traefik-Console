#!/usr/bin/env python3
"""Collect notices from the exact module cache used by the server executable."""
import hashlib,json,os,pathlib,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2]
def main():
    env=dict(os.environ,GOTOOLCHAIN='local',GOPROXY='off')
    raw=subprocess.check_output(['go','list','-buildvcs=false','-deps','-f','{{if .Module}}{{.Module.Path}}|{{.Module.Version}}|{{.Module.Dir}}{{end}}','./cmd/server'],cwd=ROOT/'backend',env=env).decode()
    modules=sorted({tuple(line.split('|')) for line in raw.splitlines() if '|' in line and line.split('|')[1]})
    texts=['# Third-party notices — core-v2\n\nVersions are retained from the existing pinned go.mod/go.sum; no dependency upgrade is selected by this correction batch. The source cache provides the exact license texts below. Modernc third-party notices include separate upstream terms; retain these with any redistribution. Runtime binaries also include the Go standard library under the Go BSD license.\n\nA symbol-level binary govulncheck scan is evidence about known Go advisories, not an OS/image scan, proof of active upstream maintenance, or an automatic upgrade recommendation. The source scanner available here cannot parse Go 1.27; that limitation remains disclosed.\n']
    records=[]
    for name,version,directory in modules:
        files=sorted(p for p in pathlib.Path(directory).iterdir() if p.is_file() and p.name.lower().startswith(('license','copying','notice')))
        if not files:raise SystemExit('Missing license for '+name)
        row={'module':name,'version':version,'license_files':[]}
        for p in files:
            b=p.read_bytes();row['license_files'].append({'name':p.name,'sha256':hashlib.sha256(b).hexdigest()});texts.append('\n## '+name+' '+version+' — '+p.name+'\n\n```text\n'+b.decode().rstrip()+'\n```\n')
        records.append(row)
    goroot=subprocess.check_output(['go','env','GOROOT'],env=env).decode().strip();go_license=pathlib.Path(goroot)/'LICENSE'
    texts.append('\n## Go standard library\n\n```text\n'+go_license.read_text().rstrip()+'\n```\n')
    # The committed recursive inventory is derived from this exact upstream baseline.
    upstream=ROOT/'.cache/upstream/src_checkout/traefik-3.7.14/LICENSE.md'
    if upstream.exists():texts.append('\n## Traefik v3.7.14 schema-derived inventory\n\n```text\n'+upstream.read_text().rstrip()+'\n```\n')
    else:raise SystemExit('Restore documented Traefik 3.7.14 source cache to regenerate its notice')
    (ROOT/'backend/THIRD-PARTY-NOTICES.md').write_text(''.join(texts))
    (ROOT/'api/schemas/dependencies.json').write_text(json.dumps({'toolchain':'go1.27.3','scope':'modules linked by cmd/server; retained pins','generator':'tools/contract/collect_licenses.py','modules':records},indent=2)+'\n')
    print('Recorded notices for',len(records),'runtime modules, Go, and Traefik inventory')
if __name__=='__main__':main()

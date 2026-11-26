# Deploying Traefik Console

Pick one folder, copy it to your server, and run `docker compose up -d` inside it.

| Folder | Starts | Use it when |
|---|---|---|
| [`traefik-console/`](traefik-console) | Traefik + Console | You don't have Traefik yet and want the minimum. |
| [`traefik-console-crowdsec/`](traefik-console-crowdsec) | Traefik + CrowdSec + Console + a socket-proxy | You want everything: CrowdSec, and Console restarting Traefik and creating CrowdSec keys itself. |
| [`console-only/`](console-only) | Console, with Docker access | You already run Traefik. |

Each folder is a complete, independent example: one `docker-compose.yml` with
every Console setting listed (the ones it uses set to example values, the rest
commented with their default), and (for the new-Traefik options) a `config/` folder
with the starting Traefik configuration. There is no `.env` file: to change a
setting, edit `docker-compose.yml`. Traefik's files stay in plain folders next to
it (`config/traefik/` and `data/`), so you can see, back up and move them. The
image is `ghcr.io/fouad-aniq/traefik-console` (linux/amd64 and linux/arm64);
release bundles pin it to the exact version.

## New Traefik

```sh
cd traefik-console            # or traefik-console-crowdsec

# First time only: create the folders Docker mounts (as your normal user, no sudo,
# so they belong to you and not to root). Console creates the rest itself.
mkdir -p config/traefik/acme data/logs data/plugins data/console
touch data/logs/access.log    # CrowdSec bundle only: CrowdSec reads this file from its start

docker compose up -d
docker compose logs console | grep TCS-   # the one-time setup token
```

Open http://localhost:8081 on the server (or tunnel it: `ssh -L 8081:localhost:8081 server`),
paste the token, and create the administrator. Console prints a new token in its
logs at every start until the administrator exists; after that it prints none.

Change the example values to yours:

- **`CONSOLE_HOST: console.example.com`** (in both `traefik` and `console`): the
  address you open Console at through Traefik. Point that name's DNS at this machine and run
  `docker compose up -d`. Traefik routes it to Console right away with its
  self-signed certificate; add a certificate resolver (Let's Encrypt, for example
  with Cloudflare) or upload a certificate from Console whenever you're ready.
  Port 8081 on 127.0.0.1 stays as a way back in if a route ever breaks.
- **`user: "1000:1000"`** (in both `traefik` and `console`): the user they run as, and who owns `config/` and
  `data/`. `1000:1000` is the first user on most Linux machines, so the files stay
  yours. If `id -u` prints another number, change it. To isolate the containers
  from your own files instead, use an unused ID such as `10001:10001` and run
  `sudo chown -R 10001:10001 config/traefik data` once.

If you skip the first-time step, Docker creates the missing folders owned by root
and Traefik and Console cannot write to them. Fix them with
`sudo chown -R $(id -u):$(id -g) config data`, then run `docker compose up -d` again.

More:

- Traefik listens on ports 80 and 443. Change them to `"127.0.0.1:80:8000"` and
  `"127.0.0.1:443:8443"` to keep Traefik private while you try things out.
- Traefik's config lives in `config/traefik/` (static file in `static/`, routes in
  `dynamic/`); Let's Encrypt files are kept in `config/traefik/acme/`. Console
  edits these files too; you can edit them directly or from Console.
- **Console's own route is yours to edit.** On its first start Console creates a
  managed `console` service and route, listed on the Services and Routes pages, where
  you can add middlewares, change the domain or subdomain, or pick a certificate
  resolver. In these bundles it copies the bootstrap route of
  `config/traefik/dynamic/management.yml`, which stays as a fallback. With your own
  Traefik it builds the route from `CONSOLE_HOST`, on the HTTPS entrypoint of your
  `traefik.yml`, to `CONSOLE_UPSTREAM` (default `http://console:8081`). It does
  nothing if another route already serves `CONSOLE_HOST`, and never recreates a
  route you deleted.
- **DNS certificate resolvers need no setup.** Traefik reads DNS provider
  credentials (Cloudflare tokens and the like) only from its environment, which is
  fixed when its container is created. So Traefik loads
  `config/traefik/dns-providers.env` (the `env_file` line), which declares every
  supported provider's variables up front, each pointing to a file in
  `config/traefik/secrets/`. When you create a resolver, Console writes that file
  and restarts Traefik. Console keeps `dns-providers.env` complete itself.
- **The all-services file** runs a socket-proxy so Console can restart Traefik
  after static changes and create CrowdSec keys, without getting the whole Docker
  socket. The proxy allows only what Console uses: inspecting containers
  (`CONTAINERS`), restarting Traefik (`POST` + `ALLOW_RESTARTS`) and running fixed
  commands (`EXEC`: `cscli` in CrowdSec, and a read check on Traefik's secret
  files). Only Console can reach it. Console finds the containers by the names
  set with `container_name`.
- **Adding CrowdSec later:** move your files into the all-services folder and start it there:
  ```sh
  cd traefik-console && docker compose down
  rm -rf ../traefik-console-crowdsec/config/traefik
  cp -a config/traefik ../traefik-console-crowdsec/config/
  cp -a data ../traefik-console-crowdsec/
  cd ../traefik-console-crowdsec && docker compose up -d
  ```
  Copy over any settings you changed in `docker-compose.yml`. Then, in Console,
  open CrowdSec and finish enrollment; Console creates the keys itself.

## Existing Traefik

```sh
cd console-only
# Edit docker-compose.yml: the values marked "your ..." (Traefik network,
# config folder, API address, container name) and the Docker socket group.
docker compose run --rm console preflight
docker compose up -d
docker compose logs console | grep TCS-   # the one-time setup token
```

`preflight` only reads. It checks the layout below and says exactly what to fix.
It also runs automatically before every start.

What your Traefik needs:

- **API:** `api: {}` reachable from Console's network at `TRAEFIK_API_URL`
  (Console reads `/api/rawdata`; keep it off public entrypoints).
- **File provider:** a watched directory (`providers.file.directory`, `watch: true`).
- **One config folder** (mounted into Console at `/config`) that contains the
  static file (outside the watched directory), the watched dynamic directory and
  a secrets directory. Mount the whole folder into Traefik too, not single files:
  Console replaces files atomically, which only works inside one mount.
- **Folder paths:** each folder has two settings, the path inside Console and the
  same folder inside Traefik (`DYNAMIC_DIR` / `TRAEFIK_DYNAMIC_DIR`,
  `SECRETS_DIR` / `TRAEFIK_SECRETS_DIR`). `TRAEFIK_DYNAMIC_DIR` must equal
  `providers.file.directory` in your `traefik.yml`.
- **Permissions:** Console runs as the `user` in the file (default `1000:1000`).
  That user needs write access to the static file's directory and to the dynamic
  and secrets directories. Console writes its files with mode 0600, so Traefik
  must run as the same user or as root (the official Traefik image runs as root
  by default). Console never changes ownership of your files.
- Your Docker labels and your own YAML files keep working; Console only writes
  files it owns and refuses names that collide with yours.

Back up the Traefik config folder before the first start: Console takes an
initial snapshot, but your files are yours.

Docker access: the file mounts the Docker socket, so Console can restart your
Traefik (found by `TRAEFIK_CONTAINER`, its container name) after static changes.
Set `group_add` to the socket's group (`stat -c %g /var/run/docker.sock`). To run
without Docker access, remove the socket volume line and the `DOCKER_HOST` and
`ALLOW_TRAEFIK_RESTART` lines; Console then asks you to restart Traefik yourself.
If you run CrowdSec, uncomment `CROWDSEC_LAPI_URL` and `CROWDSEC_CONTAINER`.

DNS certificate resolvers (Let's Encrypt with Cloudflare, OVH and so on): Traefik
reads their credentials only from its environment, fixed when its container is
created. Console keeps `dns-providers.env` in your Traefik config folder (it creates
it on startup). Add one line to your Traefik service, pointing at that file, and
recreate Traefik once with `docker compose up -d`:

```yaml
    env_file: /srv/traefik/dns-providers.env   # in your Traefik config folder
```

Your config folder must be mounted at `/etc/traefik` in Traefik (with `secrets/`
inside it). Console checks Traefik's variables and warns you until it's done.

## Day-to-day

| Task | Command (inside the folder) |
|---|---|
| Logs | `docker compose logs -f console` |
| Update | change the Console `image:` tag in `docker-compose.yml` (or download the new bundle), then `docker compose pull && docker compose up -d` |
| Reset the admin password | `docker compose exec console server admin reset-password` |
| Stop (keeps data) | `docker compose down` |
| Remove everything | `docker compose down -v` (deletes all data) |

**Backup:** stop the stack, then back up the `config/` and `data/` folders
together (`tar -czpf backup.tgz config data`), plus the CrowdSec volumes if you
use CrowdSec (`docker volume ls --filter label=com.docker.compose.project=traefik-console`).
For console-only, back up its `data/` folder and your Traefik config folder.
`data/console` holds both databases and the master key, and they must be
restored together. A configuration snapshot alone is not a full backup. Database
downgrades are not supported: keep the previous image and the matching backup
when upgrading.

## About the CrowdSec image

The all-services file uses the official image `crowdsecurity/crowdsec:v1.8.2`,
unmodified. A Trivy scan (2026-11-23) reports 20 HIGH
findings in components that image bundles (`golang.org/x/net`, `golang.org/x/text`,
`yq`, `pgproto3`, Alpine `rsync`); 18 have upstream fixes. The bundled setup turns
off the online API and Hub auto-upgrade, does not publish CrowdSec's port, and
uses SQLite (the `pgproto3` findings are in its PostgreSQL client). If that is
not acceptable for you, use `traefik-console/` and add `CROWDSEC_LAPI_URL`,
pointing at a CrowdSec you run and patch yourself, to Console's environment. The
Console image itself has no HIGH or CRITICAL findings.

## Building the image yourself

From the repository root:

```sh
docker buildx build --platform linux/amd64 --load -f deploy/Dockerfile -t traefik-console:dev .
```

Then set `image: traefik-console:dev` for the `console` service in the
`docker-compose.yml` you use.

Release check (creates and removes only its own containers and volumes):

```sh
python3 tests/integration/docker_release.py --image traefik-console:dev
```

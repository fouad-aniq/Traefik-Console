# Direct restricted Docker handler

The Console constructs `docker.Client` directly. There is no custom broker process, socket protocol or second executable.

```text
API / transaction engine -> typed Docker capability -> configured Docker socket
                                                   -> existing TCP/HTTP socket proxy -> Docker
```

The two transport alternatives use the same identity checks and fixed operations. The endpoint and registration are startup settings, never request parameters. Constructors, transport, inspection, restart, exec plumbing, secret readability and CrowdSec operations have separate files.

## Operator configuration

| Environment variable | Meaning |
|---|---|
| `DOCKER_HOST` | `unix:///var/run/docker.sock`, `tcp://socket-proxy:2375`, `http://socket-proxy:2375` or HTTPS root endpoint. Empty disables container operations. |
| `TRAEFIK_CONTAINER` | Traefik's container name (or full ID), default `traefik` when `DOCKER_HOST` is set. Resolved on every operation; the inspected container must carry exactly this name, and later steps use the full ID Docker returned. |
| `CROWDSEC_CONTAINER` | Optional CrowdSec container name; must differ from Traefik's. |
| `ALLOW_TRAEFIK_RESTART` | Explicit `true` enables restart. Default false. |
| `TRAEFIK_SECRETS_DIR` | Existing Traefik-visible registered secret root; must match the real mount. |

For example:

```sh
export DOCKER_HOST=tcp://socket-proxy:2375
export TRAEFIK_CONTAINER=traefik
export ALLOW_TRAEFIK_RESTART=true
```

Recreating a container keeps working: the name is resolved again on the next operation. Host/runtime file mounts remain explicit; changing an env mount still requires operator recreation and is not accomplished by restarting Traefik.

Old `BROKER_*` configuration is rejected with a migration message rather than silently ignored. Remove it, move the daemon/proxy address to `DOCKER_HOST`, and configure the registration above in the Console process. Stop/remove the old custom broker through the operator's normal deployment procedure; this code change does not remove existing owner containers or services. The recovered `app/core` deployment remains separate.

## Restrictions retained in the handler

- Narrow consumer interfaces; no public generic exec, raw Docker request, arbitrary target or log-export method.
- Full registered IDs and matching project/service labels checked before operations. No container identity from a form is sent to Docker.
- Fixed Traefik restart requires opt-in and confirms a running container with a newer start timestamp.
- Secret probes derive a path from the registered root plus a validated opaque name, using fixed `/usr/bin/test -r` as the container's default user.
- CrowdSec IP/CIDR/duration/reason, owned enrollment ID and 64-hex key validation stays at the execution boundary. Owned bouncer names are derived, never supplied by the browser.
- One operation lock covers the whole operation, including multi-command owned cleanup. No interleaving was introduced by removing the transport gate.
- Response/output limits, deadlines, successful finished exec status, sanitized metadata and secret-output suppression remain. Exec IDs returned by Docker must also be full immutable hex before use in another endpoint.
- No ambient HTTP proxy, redirects, endpoint credentials, query strings or arbitrary path prefixes. HTTPS uses normal system trust and TLS 1.2 minimum; no insecure certificate bypass or custom client-certificate loader is added.
- Apply/restore still belongs to the existing engine, including revisions, journal, encrypted preimages, replay, rollback and observation.

## Existing socket proxies

The handler uses ordinary Docker API paths: container inspect/restart, container exec creation, and exec start/inspection. Proxy policy must permit the operations the operator enables; read-only policies can support inspection while denying restart/exec. Denials propagate as failures and are never reported as successful changes.

The fixture uses the already-cached LinuxServer socket-proxy 3.4.4 image pinned to `sha256:a8c700baae19c2fff5b2940b21a748e78cf3b91cc311ad65f990efabdd1361eb`, with container/exec access and POST only in its write-enabled case. The upstream configuration and GPL-3.0 license are documented in the [official repository](https://github.com/linuxserver/docker-socket-proxy). The image is an external test dependency, not bundled application code or a new runtime requirement. No OS vulnerability clearance is claimed.

The user explicitly chose in-process restrictions instead of a separate privileged broker. With a direct socket, the Console process has Docker access; a generic socket proxy filters endpoints but does not re-create the removed process isolation or enforce the handler's exact target/command rules independently. Keep the endpoint operator-controlled and privately reachable. Authentication remains deferred for this development candidate.

## Verification

Unit tests cover transport policies, disabled mode, registration/role mismatches, restart opt-in, input/exec-ID rejection before effects, explicit exit status, redirection, typed operation surface, secret-env projection and operation-wide cleanup locking. The real isolated lab runs on both Unix and TCP transports; TCP additionally proves a read-only proxy allows inspection, denies restart and leaves traffic/start time unchanged. Public DNS issuance, live CrowdSec protection and HTTPS proxy certificate integration remain separate checks.

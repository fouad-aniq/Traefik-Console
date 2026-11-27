# Notice

Traefik Console
Copyright (c) 2026 Fouad Aniq
Licensed under the MIT License. See [LICENSE](LICENSE).

## Relationship to Traefik and CrowdSec

Traefik Console is an independent project. It is not affiliated with, endorsed by
or sponsored by Traefik Labs or CrowdSec SAS.

- "Traefik" and "Traefik Proxy" are trademarks of Traefik Labs.
- "CrowdSec" is a trademark of CrowdSec SAS.

These names are used only to identify the software Traefik Console works with.
The Traefik Console name, logo and icons in [`docs/assets/brand`](docs/assets/brand)
and [`frontend/public/assets`](frontend/public/assets) are original artwork and do
not include the logo of Traefik, Traefik Labs or CrowdSec.

## Software that is not part of this project

Traefik Console does not include, modify or redistribute Traefik Proxy, CrowdSec or
any Traefik plugin. The Docker image published for this project contains Traefik
Console only.

The example Compose files in [`deploy/`](deploy) reference other images that Docker
pulls directly from their publishers. Each is distributed under its own license:

| Software | Image in the examples | License |
|---|---|---|
| Traefik Proxy | `traefik` | MIT (Traefik Labs) |
| CrowdSec | `crowdsecurity/crowdsec` | MIT (CrowdSec SAS) |
| Socket proxy | `lscr.io/linuxserver/socket-proxy` | GPL-3.0 (LinuxServer.io) |
| CrowdSec bouncer Traefik plugin | downloaded by Traefik at runtime, when enabled | Apache-2.0 (its authors) |

When you enable a Traefik plugin from the catalog, Traefik downloads it from its
author. Its license is the one in that plugin's repository.

## Third-party code included in Traefik Console

Console includes open-source libraries and data under their own licenses. The full
license texts are in:

- [`backend/THIRD-PARTY-NOTICES.md`](backend/THIRD-PARTY-NOTICES.md): Go modules,
  the Go standard library, and the middleware field inventory derived from
  Traefik v3.7.14 (MIT, Copyright (c) 2016-2020 Containous SAS; 2020-2025 Traefik Labs).
- [`frontend/THIRD-PARTY-NOTICES.md`](frontend/THIRD-PARTY-NOTICES.md): the YAML
  editor (CodeMirror, Lezer) and the world map outline used on the CrowdSec page
  (world-atlas, from Natural Earth data).

The frontend also bundles a copy of the same Traefik-derived middleware field
inventory (`frontend/src/features/middlewares/schema.json`), covered by the Traefik
MIT notice in `backend/THIRD-PARTY-NOTICES.md`.

The wordmark in the logo is set in Inter (SIL Open Font License 1.1) and converted
to outlines. No font file is distributed.

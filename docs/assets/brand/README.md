# Brand assets

The mark is the one drawn in the app header (`frontend/src/shared/ui/TrafficMark.svelte`):
an entry point on the left and one path splitting to a route and a service.

| File | Use |
|---|---|
| `logo.svg`, `logo.png` | Horizontal logo for light backgrounds |
| `logo-dark.svg`, `logo-dark.png` | Horizontal logo for dark backgrounds |
| `logo-tile.svg`, `logo-tile-dark.svg` (+ `.png`) | Horizontal logo with the app icon |
| `logo-icon.svg`, `logo-icon-{64,128,256,512,1024}.png` | App icon (rounded tile) |
| `logo-mark.svg`, `logo-mark-dark.svg` (+ `-256`, `-512` `.png`) | Mark only, transparent background |
| `social-preview.png` | GitHub social preview, 1280×640 (Settings → General → Social preview) |

Favicons, the Apple touch icon and the web app manifest live in
`frontend/public/assets/` and are linked from `frontend/index.html`.

Colors come from `frontend/src/app.css`:

| Name | Hex |
|---|---|
| Navy (icon tile) | `#13242E` |
| Ink | `#1B303C` |
| Teal | `#176D7D` / `#7FC4D0` on dark |
| Amber | `#895414` / `#E8B04A` on dark |

The wordmark is Inter Display (SemiBold and Regular), converted to outlines.
`tools/brand/make_brand.py` regenerates every SVG.

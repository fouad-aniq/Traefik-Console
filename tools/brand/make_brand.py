"""Builds the Traefik Console brand set from the in-app TrafficMark.

The mark is the one already drawn in frontend/src/shared/ui/TrafficMark.svelte
(an entry point on the left, one path splitting to a route and a service);
this script only re-draws it at icon sizes and adds the outlined wordmark."""
import os
from fontTools.ttLib import TTFont
from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen

OUT = os.path.join(os.path.dirname(__file__), 'out')
os.makedirs(OUT, exist_ok=True)

# Palette, from frontend/src/app.css
NAVY = '#13242E'      # tile background (a touch darker than --text #1B303C)
INK = '#1B303C'       # --text, light theme
TEAL = '#176D7D'      # --connection, light theme
TEAL_LIGHT = '#7FC4D0'  # connection on dark backgrounds
AMBER = '#895414'     # --accent, light theme
AMBER_LIGHT = '#E8B04A'  # --accent, dark theme
WHITE = '#FFFFFF'
MUTED = '#4D6573'
MUTED_DARK = '#9DB3BF'


def mark(line, dot, node_fill, sw=1.75):
    """The mark in its own 32x32 space."""
    return (f'<path d="M7 7v12a6 6 0 0 0 6 6h12M7 13h9a6 6 0 0 0 6-6" stroke="{line}" stroke-width="{sw}" stroke-linecap="round" fill="none"/>'
            f'<circle cx="7" cy="6" r="3" fill="{dot}"/>'
            f'<circle cx="22" cy="6" r="3" fill="{node_fill}" stroke="{line}" stroke-width="{sw}"/>'
            f'<rect x="22" y="22" width="6" height="6" rx="1.5" fill="{node_fill}" stroke="{line}" stroke-width="{sw}"/>')

# The drawn mark spans x 4..29.9, y 3..28.9 (with strokes): centre it.
CX, CY = 16.9, 15.95


def svg(w, h, body, title='Traefik Console'):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" viewBox="0 0 {w} {h}" role="img" aria-label="{title}">'
            f'<title>{title}</title>{body}</svg>\n')


def tile(size=512, radius=None, sw=2.4, scale=None, bg=NAVY):
    radius = size * 0.225 if radius is None else radius
    scale = scale or size / 32 * 0.66
    t = f'translate({size/2} {size/2}) scale({scale:.4f}) translate({-CX} {-CY})'
    rect = f'<rect width="{size}" height="{size}" rx="{radius:.1f}" fill="{bg}"/>' if bg else ''
    return rect + f'<g transform="{t}">' + mark(TEAL_LIGHT, AMBER_LIGHT, bg or NAVY, sw) + '</g>'


def free_mark(size, line, dot, fill, sw=1.75, pad=0.06):
    scale = size * (1 - 2 * pad) / 26.5
    t = f'translate({size/2} {size/2}) scale({scale:.4f}) translate({-CX} {-CY})'
    return f'<g transform="{t}">' + mark(line, dot, fill, sw) + '</g>'


def text_path(text, font_file, size, x, y, fill, tracking=0):
    font = TTFont(font_file)
    gs = font.getGlyphSet(); cmap = font.getBestCmap(); upm = font['head'].unitsPerEm
    s = size / upm
    out, cursor = [], x
    kern = None
    for ch in text:
        g = cmap[ord(ch)]
        pen = SVGPathPen(gs)
        gs[g].draw(TransformPen(pen, (s, 0, 0, -s, cursor, y)))
        d = pen.getCommands()
        if d: out.append(d)
        cursor += gs[g].width * s + tracking
    return f'<path fill="{fill}" d="{" ".join(out)}"/>', cursor - x


FONT_SEMI = '/usr/share/fonts/opentype/inter/InterDisplay-SemiBold.otf'
FONT_REG = '/usr/share/fonts/opentype/inter/InterDisplay-Regular.otf'
for f in (FONT_SEMI, FONT_REG):
    assert os.path.exists(f), f


def wordmark(theme='light', height=96):
    ink, muted = (INK, MUTED) if theme == 'light' else (WHITE, MUTED_DARK)
    line, dot, fill = (TEAL, AMBER, 'none') if theme == 'light' else (TEAL_LIGHT, AMBER_LIGHT, 'none')
    icon = height
    fs = height * 0.42
    base = height / 2 + fs * 0.36
    gap = height * 0.10
    p1, w1 = text_path('Traefik', FONT_SEMI, fs, icon + gap, base, ink, tracking=-fs * 0.012)
    p2, w2 = text_path('Console', FONT_REG, fs, icon + gap + w1 + fs * 0.26, base, muted, tracking=-fs * 0.012)
    width = icon + gap + w1 + fs * 0.26 + w2 + 2
    bg_fill = 'none'
    body = free_mark(icon, line, dot, '#FFFFFF' if theme == 'light' else NAVY, sw=1.9, pad=0.13) + p1 + p2
    return svg(round(width), height, body)


def wordmark_on_tile(theme='light', height=96):
    """Horizontal logo with the app-icon tile instead of the free mark."""
    ink, muted = (INK, MUTED) if theme == 'light' else (WHITE, MUTED_DARK)
    fs = height * 0.42
    base = height / 2 + fs * 0.36
    gap = height * 0.26
    p1, w1 = text_path('Traefik', FONT_SEMI, fs, height + gap, base, ink, tracking=-fs * 0.012)
    p2, w2 = text_path('Console', FONT_REG, fs, height + gap + w1 + fs * 0.26, base, muted, tracking=-fs * 0.012)
    width = height + gap + w1 + fs * 0.26 + w2 + 2
    t = tile(512)
    body = f'<g transform="scale({height/512:.5f})">{t}</g>' + p1 + p2
    return svg(round(width), height, body)


def social(theme='dark'):
    w, h = 1280, 640
    bg = NAVY if theme == 'dark' else '#F4F7F8'
    ink, muted = (WHITE, MUTED_DARK) if theme == 'dark' else (INK, MUTED)
    body = f'<rect width="{w}" height="{h}" fill="{bg}"/>'
    # faint grid of dots, like the map canvas
    dots = ''.join(f'<circle cx="{x}" cy="{y}" r="1.6"/>' for x in range(40, w, 40) for y in range(40, h, 40))
    body += f'<g fill="{"#FFFFFF" if theme=="dark" else INK}" opacity="0.07">{dots}</g>'
    icon = 200
    body += f'<g transform="translate(140 {h/2 - icon/2 - 40}) scale({icon/512:.5f})">{tile(512, bg="#1D3542" if theme=="dark" else NAVY)}</g>'
    p1, w1 = text_path('Traefik Console', FONT_SEMI, 100, 140 + icon + 56, h / 2 - 40 + 36, ink, tracking=-1.2)
    p2, _ = text_path('A web UI for Traefik’s file provider: preview,', FONT_REG, 40, 140, h / 2 + 140, muted)
    p3, _ = text_path('apply, verify and roll back every change.', FONT_REG, 40, 140, h / 2 + 196, muted)
    return svg(w, h, body.split('</g>',1)[0] + '</g>' + '<g transform="translate(0 -34)">' + body.split('</g>',1)[1] + p1 + p2 + p3 + '</g>')


files = {
    'logo-mark.svg': svg(32, 32, mark(TEAL, AMBER, '#FFFFFF')),
    'logo-mark-dark.svg': svg(32, 32, mark(TEAL_LIGHT, AMBER_LIGHT, NAVY)),
    'logo-icon.svg': svg(512, 512, tile(512)),
    'logo.svg': wordmark('light'),
    'logo-dark.svg': wordmark('dark'),
    'logo-tile.svg': wordmark_on_tile('light'),
    'logo-tile-dark.svg': wordmark_on_tile('dark'),
    'social-preview.svg': social('dark'),
    # favicon: tile with heavier strokes and less padding so it holds at 16 px
    'favicon.svg': svg(32, 32, tile(32, radius=7, sw=2.9, scale=32 / 32 * 0.80)),
    # maskable: full-bleed background, mark inside the 80% safe zone
    'maskable.svg': svg(512, 512, tile(512, radius=0, scale=512 / 32 * 0.52)),
}
for name, content in files.items():
    open(os.path.join(OUT, name), 'w').write(content)
print('\n'.join(files))

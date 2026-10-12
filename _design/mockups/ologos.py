# ologos.py: stylised-O logo concepts for Oriel → src/34-o-logos.html
import math
G = "#39ff88"

def ring_poly(n, r1, r2, rot=0):
    o = [(50 + r1 * math.cos(math.radians(rot + 360 * k / n)), 50 + r1 * math.sin(math.radians(rot + 360 * k / n))) for k in range(n)]
    i = [(50 + r2 * math.cos(math.radians(rot + 360 * k / n)), 50 + r2 * math.sin(math.radians(rot + 360 * k / n))) for k in range(n)]
    pts = lambda p: " ".join(f"{x:.2f},{y:.2f}" for x, y in p)
    return o, i, pts

def hex_o():
    o, i, pts = ring_poly(6, 40, 24, -90)
    return f'<polygon points="{pts(o)}" fill="{G}" fill-opacity=".14"/><polygon points="{pts(i)}" fill="#0b0f15"/>'

def facet_o():
    o, i, pts = ring_poly(8, 41, 25, -22.5)
    s = f'<polygon points="{pts(o)}" fill="{G}" fill-opacity=".1"/><polygon points="{pts(i)}" fill="#0b0f15"/>'
    for k in range(8):
        a, b = o[k], i[k]
        c = i[(k + 1) % 8]
        s += f'<path d="M{a[0]:.1f} {a[1]:.1f} L{b[0]:.1f} {b[1]:.1f} M{a[0]:.1f} {a[1]:.1f} L{c[0]:.1f} {c[1]:.1f}" stroke-opacity=".7" stroke-width=".9"/>'
    return s

def cursor_o():
    return (f'<path d="M73 30 A30 30 0 1 0 80 52" fill="none" stroke-width="9" stroke-linecap="butt"/>'
            f'<rect x="73" y="35" width="11" height="14" fill="{G}" stroke="none"/>')

def gauge_o():
    s = ""
    for k in range(12):
        a0, a1 = -90 + k * 30 + 3, -90 + (k + 1) * 30 - 3
        p = lambda a, r: (50 + r * math.cos(math.radians(a)), 50 + r * math.sin(math.radians(a)))
        x0, y0 = p(a0, 36); x1, y1 = p(a1, 36)
        op = "1" if k < 8 else ".25"
        s += f'<path d="M{x0:.1f} {y0:.1f} A36 36 0 0 1 {x1:.1f} {y1:.1f}" fill="none" stroke-width="8" stroke-opacity="{op}"/>'
    return s + '<circle cx="50" cy="50" r="22" fill="none" stroke-opacity=".5"/>'

def brackets_o():
    return ('<path d="M44 14 A36 36 0 0 0 44 86" fill="none" stroke-width="8" stroke-linecap="round"/>'
            '<path d="M56 14 A36 36 0 0 1 56 86" fill="none" stroke-width="8" stroke-linecap="round"/>'
            f'<circle cx="50" cy="50" r="4.5" fill="{G}" stroke="none"/>')

def lens_o():
    return ('<circle cx="50" cy="50" r="36" fill="none" stroke-width="7"/>'
            '<circle cx="50" cy="50" r="18" fill="none" stroke-opacity=".55"/>'
            f'<circle cx="50" cy="50" r="6" fill="{G}" stroke="none"/>'
            '<path d="M36 28 A26 26 0 0 1 60 25" fill="none" stroke-width="2.5" stroke-opacity=".8"/>')

def bay_o():
    # round on the left, three straight facets on the right — a bay window in plan
    return ('<path d="M50 14 A36 36 0 0 0 50 86 L72 78 L86 50 L72 22 Z" fill="' + G + '" fill-opacity=".1" stroke-width="7" stroke-linejoin="round"/>'
            '<path d="M58 50 L86 50 M72 22 L66 50 L72 78" fill="none" stroke-opacity=".55" stroke-width="1.6"/>')

def stencil_o():
    return ('<path d="M46 14.2 A36 36 0 0 0 46 85.8" fill="none" stroke-width="12"/>'
            '<path d="M54 14.2 A36 36 0 0 1 54 85.8" fill="none" stroke-width="12"/>')

def reticle_o():
    t = ""
    for a in (0, 90, 180, 270):
        x0 = 50 + 40 * math.cos(math.radians(a)); y0 = 50 + 40 * math.sin(math.radians(a))
        x1 = 50 + 48 * math.cos(math.radians(a)); y1 = 50 + 48 * math.sin(math.radians(a))
        t += f'<path d="M{x0:.1f} {y0:.1f} L{x1:.1f} {y1:.1f}" stroke-width="3"/>'
    return '<circle cx="50" cy="50" r="32" fill="none" stroke-width="7"/>' + t

def split_o():
    return ('<circle cx="47" cy="50" r="32" fill="none" stroke="#5cd2ff" stroke-width="7" stroke-opacity=".9"/>'
            '<circle cx="53" cy="50" r="32" fill="none" stroke-width="7" style="mix-blend-mode:screen"/>')

def pixel_o():
    P = ["..XXXXX..", ".XX...XX.", "XX.....XX", "XX.....XX", "XX.....XX", "XX.....XX", "XX.....XX", ".XX...XX.", "..XXXXX.."]
    s = ""
    for y, row in enumerate(P):
        for x, c in enumerate(row):
            if c == "X":
                s += f'<rect x="{14 + x * 8.1:.1f}" y="{14 + y * 8.1:.1f}" width="7.2" height="7.2" fill="{G}" stroke="none"/>'
    return s

def prompt_o():
    return ('<circle cx="40" cy="54" r="22" fill="none" stroke-width="8"/>'
            f'<rect x="70" y="30" width="12" height="46" fill="{G}" stroke="none"/>')

concepts = [
    ("O1", "Hex ring", "a faceted hexagonal O: solid and techy", hex_o),
    ("O2", "Faceted ring", "an octagonal O split into low-poly facets", facet_o),
    ("O3", "Cursor O", "an O that ends in a block cursor", cursor_o),
    ("O4", "Gauge", "a HUD dial: 8 of 12 segments lit", gauge_o),
    ("O5", "Brackets", "two arcs, ( · ), around a point", brackets_o),
    ("O6", "Lens", "an O with a lens: the window into the code", lens_o),
    ("O7", "Bay O", "round on one side, faceted on the other: a bay window in plan", bay_o),
    ("O8", "Stencil", "a heavy O with stencil gaps", stencil_o),
    ("O9", "Reticle", "an O with sight ticks: focus", reticle_o),
    ("O10", "Split", "chromatic offset, cyan and green", split_o),
    ("O11", "Pixel", "a 9×9 sprite", pixel_o),
    ("O12", "o▌", "a lowercase o with a block cursor: the editor itself", prompt_o),
]

def svg(inner, size, sw=4.5, glow=True, col=G):
    f = 'filter="url(#glow)"' if glow else ''
    return (f'<svg width="{size}" height="{size}" viewBox="0 0 100 100"><defs><filter id="glow" x="-30%" y="-30%" width="160%" height="160%">'
            f'<feGaussianBlur stdDeviation="2" result="b"/><feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter></defs>'
            f'<g stroke="{col}" stroke-width="{sw}" fill="none" {f}>{inner}</g></svg>')

cards = ""
for k, name, desc, fn in concepts:
    inner = fn()
    light = inner.replace(G, "#0a7a43").replace("#0b0f15", "#eef2ee").replace("#5cd2ff", "#1f6fa8")
    cards += (f'<div class="card"><div class="hd"><span class="k">{k}</span><span class="nm">{name}</span><span class="ds">{desc}</span></div>'
              f'<div class="big">{svg(inner, 150)}</div>'
              f'<div class="sizes"><span>{svg(inner, 48)}</span><span>{svg(inner, 32, glow=False)}</span><span>{svg(inner, 16, glow=False)}</span>'
              f'<span class="tb">{svg(inner, 18, glow=False)}<b>ORIEL</b></span>'
              f'<span class="lt">{svg(light, 26, glow=False, col="#0a7a43")}<i>oriel</i></span></div></div>')

html = f'''<!doctype html><meta charset="utf-8"><link rel="stylesheet" href="base.css">
<style>
html,body{{height:1000px}}
body{{background:radial-gradient(1200px 600px at 50% 0%,#0d1712,var(--bg0))}}
.top{{padding:20px 30px 2px;display:flex;align-items:baseline;gap:14px}}
.top h1{{font:600 22px var(--disp);color:var(--fg)}}
.top span{{color:var(--fg3);font-size:13px}}
.grid{{display:grid;grid-template-columns:repeat(4,1fr);gap:14px;padding:12px 30px}}
.card{{background:rgba(11,15,21,.8);border:1px solid var(--line);border-radius:10px;overflow:hidden}}
.hd{{display:flex;align-items:baseline;gap:8px;padding:8px 12px;border-bottom:1px solid var(--line);height:34px}}
.hd .k{{font:700 11px var(--mono);color:{G}}}
.hd .nm{{font:600 13px var(--disp);color:var(--fg);white-space:nowrap}}
.hd .ds{{font-size:10.5px;color:var(--fg3);margin-left:auto;text-align:right;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}}
.big{{height:170px;display:grid;place-items:center;background:radial-gradient(circle,rgba(57,255,136,.06),transparent 60%)}}
.sizes{{display:flex;align-items:center;gap:10px;padding:8px 12px;border-top:1px solid var(--line)}}
.sizes svg{{display:block}}
.tb{{display:flex;align-items:center;gap:6px;background:var(--bg1);border:1px solid var(--line2);border-radius:5px;padding:4px 8px;margin-left:auto}}
.tb b{{font:700 10px var(--disp);letter-spacing:.2em;color:var(--fg)}}
.lt{{display:flex;align-items:center;gap:5px;background:#eef2ee;border-radius:5px;padding:3px 7px}}
.lt i{{font:600 15px var(--disp);font-style:normal;color:#0d1712}}
</style>
<div class="top"><h1>Oriel · stylised-O concepts</h1><span>neon #39ff88 (recolours to cyan with one token) · 150 · 48 · 32 · 16 px · titlebar · wordmark on light</span></div>
<div class="grid">{cards}</div>'''
open("src/34-o-logos.html", "w").write(html)

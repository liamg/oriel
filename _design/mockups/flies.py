# flies.py: house-fly logo concepts → src/31-flies.html
import math
G = "#39ff88"

def hexgrid(cx, cy, rx, ry, r, cid):
    out = []
    for row in range(-12, 13):
        for col in range(-12, 13):
            x = cx + col * r * math.sqrt(3) + (row % 2) * r * math.sqrt(3) / 2
            y = cy + row * r * 1.5
            if ((x - cx) / (rx + r)) ** 2 + ((y - cy) / (ry + r)) ** 2 > 1:
                continue
            pts = " ".join(f"{x + (r - .35) * math.cos(math.radians(60 * k + 30)):.2f},{y + (r - .35) * math.sin(math.radians(60 * k + 30)):.2f}" for k in range(6))
            out.append(f'<polygon points="{pts}"/>')
    return f'<g clip-path="url(#{cid})" stroke-width=".55" stroke-opacity=".85">' + "".join(out) + "</g>"

def mirror(s):
    return f'<g transform="translate(100,0) scale(-1,1)">{s}</g>'

# --- top-down house fly, wings folded back in a V ---
def legs_top():
    l = ('<path d="M38 34 L28 28 L24 18"/>'
         '<path d="M37 43 L24 44 L15 52"/>'
         '<path d="M39 50 L29 60 L25 74"/>')
    return l + mirror(l)

def eyes_top(mesh, cid):
    e = '<ellipse cx="41.5" cy="21" rx="8.5" ry="8"/>'
    clip = f'<clipPath id="{cid}"><ellipse cx="41.5" cy="21" rx="8.5" ry="8"/><ellipse cx="58.5" cy="21" rx="8.5" ry="8"/></clipPath>'
    fill = f'<g fill="{G}" fill-opacity=".14">' + e + mirror(e) + '</g>'
    m = hexgrid(41.5, 21, 8.5, 8, 1.7, cid) + mirror(hexgrid(41.5, 21, 8.5, 8, 1.7, cid)) if mesh else ""
    return clip + fill + m

def body_top(stripes=True):
    s = ('<path d="M50 28 C60 28 64 34 64 41 C64 49 58 53 50 53 C42 53 36 49 36 41 C36 34 40 28 50 28 Z"/>'
         '<path d="M50 52 C60 52 64 58 64 65 C64 74 57 80 50 80 C43 80 36 74 36 65 C36 58 40 52 50 52 Z"/>')
    if stripes:
        s += '<path d="M45 31 L45 50 M50 30 L50 51 M55 31 L55 50" stroke-opacity=".7"/>'
        s += '<path d="M39 62 Q50 66 61 62 M38 70 Q50 74 62 70" stroke-opacity=".55"/>'
    return s

def wings_back(mesh):
    w = '<path d="M44 38 C38 46 31 62 31 78 C31 88 39 90 42 82 C45 70 47 54 47 42 Z" fill="' + G + '" fill-opacity=".07"/>'
    v = ('<path d="M45 40 C40 54 35 68 34 82" stroke-opacity=".75"/>'
         '<path d="M46.5 42 C43 56 40 70 39 84" stroke-opacity=".75"/>'
         '<path d="M36.5 66 L42.5 64" stroke-opacity=".75"/>') if mesh else ""
    return w + v + mirror(w + v)

def fly_top(mesh, cid):
    return ('<g fill="none" stroke-linecap="round" stroke-linejoin="round">'
            + legs_top() + body_top(stripes=mesh) + wings_back(mesh) + eyes_top(mesh, cid) +
            '<path d="M47 13 L45 9 M53 13 L55 9"/></g>')

# --- top-down, wings spread (in flight) ---
def wings_spread():
    w = ('<path d="M42 37 C34 30 16 28 8 34 C3 39 8 46 18 47 C28 48 38 45 43 42 Z" fill="' + G + '" fill-opacity=".07"/>'
         '<path d="M42 38 C30 34 18 34 10 37" stroke-opacity=".75"/>'
         '<path d="M42 40 C32 41 22 42 14 44" stroke-opacity=".75"/>'
         '<path d="M26 35 L24 44" stroke-opacity=".75"/>')
    return w + mirror(w)

def fly_spread(cid):
    return ('<g fill="none" stroke-linecap="round" stroke-linejoin="round">'
            + legs_top() + body_top() + wings_spread() + eyes_top(True, cid) +
            '<path d="M47 13 L45 9 M53 13 L55 9"/></g>')

# --- the face: two huge compound eyes ---
def face(cid):
    clip = f'<clipPath id="{cid}"><ellipse cx="32" cy="44" rx="20" ry="24"/><ellipse cx="68" cy="44" rx="20" ry="24"/></clipPath>'
    eyes = '<ellipse cx="32" cy="44" rx="20" ry="24"/>' + '<ellipse cx="68" cy="44" rx="20" ry="24"/>'
    return ('<g fill="none" stroke-linecap="round" stroke-linejoin="round">' + clip +
            f'<g fill="{G}" fill-opacity=".12">{eyes}</g>' +
            hexgrid(32, 44, 20, 24, 3.1, cid) + hexgrid(68, 44, 20, 24, 3.1, cid) +
            '<path d="M44 66 C47 70 53 70 56 66"/>'
            '<path d="M50 69 L50 82"/><path d="M44 82 C44 88 56 88 56 82 Z"/>'
            '<path d="M46 22 L42 12 M54 22 L58 12"/></g>')

# --- side profile ---
def side(cid):
    clip = f'<clipPath id="{cid}"><ellipse cx="23" cy="47" rx="9" ry="11"/></clipPath>'
    return ('<g fill="none" stroke-linecap="round" stroke-linejoin="round">' + clip +
            '<path d="M30 38 C34 28 50 26 58 34 C62 40 62 50 58 56 C52 62 38 62 32 56 C28 52 28 44 30 38 Z"/>'   # thorax hump
            '<path d="M57 44 C66 40 82 44 86 54 C88 62 80 68 70 66 C62 65 58 60 57 54"/>'                       # abdomen
            '<path d="M34 46 C30 44 24 36 17 40 C12 44 12 54 17 58 C22 62 30 56 34 52"/>'                       # head
            f'<g fill="{G}" fill-opacity=".14"><ellipse cx="23" cy="47" rx="9" ry="11"/></g>' + hexgrid(23, 47, 9, 11, 1.8, cid) +
            '<path d="M20 58 L17 68 L13 70"/>'                                                                  # proboscis
            f'<path d="M46 31 C58 22 80 24 94 32 C96 36 90 39 80 39 C66 39 54 37 48 35 Z" fill="{G}" fill-opacity=".07"/>'  # wing
            '<path d="M48 33 C62 29 78 29 92 33 M52 35 C64 34 76 35 86 37" stroke-opacity=".7"/>'
            '<path d="M38 58 L32 72 L26 84 M46 60 L46 74 L42 86 M54 58 L62 72 L66 86"/>'                       # legs
            '<path d="M42 32 L44 28 M50 30 L52 26" stroke-opacity=".7"/>'                                       # bristles
            '</g>')

# --- simple silhouette for 16px ---
def tiny():
    legs = '<path d="M38 36 L26 28 L24 18 M37 44 L22 44 L12 52 M39 52 L28 62 L24 76"/>'
    wing = '<path d="M43 40 C32 50 22 66 22 80 C22 90 32 90 36 82 C42 68 46 54 47 44 Z"/>'
    return (f'<g fill="none" stroke="{G}" stroke-width="5" stroke-linecap="round" stroke-linejoin="round">' + legs + mirror(legs) + '</g>'
            f'<g fill="{G}" fill-opacity=".5" stroke="none">' + wing + mirror(wing) + '</g>'
            f'<g fill="{G}" stroke="none">'
            '<ellipse cx="39" cy="21" rx="11" ry="10"/><ellipse cx="61" cy="21" rx="11" ry="10"/>'
            '<ellipse cx="50" cy="42" rx="14" ry="12"/><ellipse cx="50" cy="65" rx="12" ry="14"/></g>')

concepts = [
    ("1", "House fly · resting", "top-down, wings folded in a V, faceted eyes, striped thorax", lambda c: fly_top(True, c)),
    ("2", "House fly · clean", "same silhouette, no mesh: holds up at small sizes", lambda c: fly_top(False, c)),
    ("3", "House fly · flying", "wings spread, same stout body", fly_spread),
    ("4", "The face", "two huge compound eyes and a proboscis", face),
    ("5", "Side profile", "hunched thorax, big eye, wings back, legs down", side),
    ("6", "Solid mark", "filled silhouette for 16 px, favicon and dock", lambda c: tiny()),
]

def svg(inner, size, sw, glow=True, col=G, uid=""):
    f = 'filter="url(#glow)"' if glow else ''
    return (f'<svg width="{size}" height="{size}" viewBox="0 0 100 100"><defs><filter id="glow" x="-30%" y="-30%" width="160%" height="160%">'
            f'<feGaussianBlur stdDeviation="1.8" result="b"/><feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter></defs>'
            f'<g stroke="{col}" stroke-width="{sw}" {f}>{inner}</g></svg>')

cards = ""
for k, name, desc, fn in concepts:
    big = svg(fn(f"c{k}a"), 230, 1.3)
    sizes = (f'<span>{svg(fn(f"c{k}b"), 64, 1.9)}</span>'
             f'<span>{svg(fn(f"c{k}c"), 32, 2.6)}</span>'
             f'<span>{svg(tiny() if k in "12356" else fn(f"c{k}d"), 16, 3, glow=False)}</span>'
             f'<span class="tb">{svg(fn(f"c{k}e"), 20, 3, glow=False)}<b>FLY</b></span>'
             f'<span class="lt">{svg(fn(f"c{k}f").replace(G, "#0a7a43"), 34, 2.2, glow=False, col="#0a7a43")}</span>')
    cards += (f'<div class="card"><div class="hd"><span class="k">{k}</span><span class="nm">{name}</span><span class="ds">{desc}</span></div>'
              f'<div class="big">{big}</div><div class="sizes">{sizes}</div></div>')

html = f'''<!doctype html><meta charset="utf-8"><link rel="stylesheet" href="base.css">
<style>
html,body{{height:1000px}}
body{{background:radial-gradient(1200px 600px at 50% 0%,#0d1712,var(--bg0))}}
.top{{padding:24px 34px 4px;display:flex;align-items:baseline;gap:14px}}
.top h1{{font:600 22px var(--disp);color:var(--fg)}}
.top span{{color:var(--fg3);font-size:13px}}
.grid{{display:grid;grid-template-columns:repeat(3,1fr);gap:18px;padding:16px 34px 30px}}
.card{{background:rgba(11,15,21,.8);border:1px solid var(--line);border-radius:10px;overflow:hidden}}
.hd{{display:flex;align-items:baseline;gap:8px;padding:10px 14px;border-bottom:1px solid var(--line)}}
.hd .k{{font:700 12px var(--mono);color:{G}}}
.hd .nm{{font:600 14px var(--disp);color:var(--fg);white-space:nowrap}}
.hd .ds{{font-size:11px;color:var(--fg3);margin-left:auto;text-align:right}}
.big{{height:290px;display:grid;place-items:center;background:radial-gradient(circle at 50% 50%,rgba(57,255,136,.06),transparent 60%)}}
.sizes{{display:flex;align-items:center;gap:14px;padding:10px 14px;border-top:1px solid var(--line)}}
.sizes svg{{display:block}}
.tb{{display:flex;align-items:center;gap:7px;background:var(--bg1);border:1px solid var(--line2);border-radius:5px;padding:5px 9px;margin-left:auto}}
.tb b{{font:700 11px var(--disp);letter-spacing:.2em;color:var(--fg)}}
.lt{{background:#eef2ee;border-radius:5px;padding:4px;display:grid;place-items:center}}
</style>
<div class="top"><h1>Fly · house-fly logo concepts</h1><span>big compound eyes · stout striped thorax · short round abdomen · two clear wings in a V · six legs</span></div>
<div class="grid">{cards}</div>'''
open("src/31-flies.html", "w").write(html)

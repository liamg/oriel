# faces.py: fly-face logo concepts → src/32-faces.html
import math
from flies import hexgrid, mirror, G

EYE = (29.5, 41, 18, 24, -9)        # cx, cy, rx, ry, rotation

def eye_shape(fill=None):
    cx, cy, rx, ry, rot = EYE
    f = f'fill="{G}" fill-opacity="{fill}"' if fill else ''
    e = f'<ellipse cx="{cx}" cy="{cy}" rx="{rx}" ry="{ry}" transform="rotate({rot} {cx} {cy})" {f}/>'
    return e + mirror(e)

def eye_clip(cid):
    cx, cy, rx, ry, rot = EYE
    e = f'<ellipse cx="{cx}" cy="{cy}" rx="{rx}" ry="{ry}" transform="rotate({rot} {cx} {cy})"/>'
    return f'<clipPath id="{cid}">{e}{mirror(e)}</clipPath>'

def head_parts(arista=True):
    s = ('<path d="M46 20 C47 34 47 46 46 54 M54 20 C53 34 53 46 54 54" stroke-opacity=".6"/>'      # frons
         '<path d="M35 64 C42 70 58 70 65 64 C62 72 56 76 50 76 C44 76 38 72 35 64 Z"/>'               # face plate
         '<path d="M47.5 55 L44 63 M52.5 55 L56 63"/>'                                                 # antennae
         '<path d="M50 76 L50 84"/>'
         '<path d="M42 87 C42 82 58 82 58 87 C58 92 42 92 42 87 Z"/>'                                  # labellum
         '<path d="M46 85 L46 90 M50 84 L50 91 M54 85 L54 90" stroke-opacity=".55"/>')
    if arista:
        s += '<path d="M44 63 L39 66 M56 63 L61 66" stroke-opacity=".6"/>'
    return s

def lowpoly(cx, cy, rx, ry, rot):
    pts = []
    for k in range(10):
        a = math.radians(36 * k)
        x, y = rx * math.cos(a), ry * math.sin(a)
        r = math.radians(rot)
        pts.append((cx + x * math.cos(r) - y * math.sin(r), cy + x * math.sin(r) + y * math.cos(r)))
    inner = [(cx + (px - cx) * .5, cy + (py - cy) * .5) for px, py in pts[::2]]
    l = ""
    for i, (px, py) in enumerate(pts):
        a, b = inner[(i // 2) % 5], inner[((i + 1) // 2) % 5]
        l += f'<path d="M{px:.1f} {py:.1f} L{a[0]:.1f} {a[1]:.1f} M{px:.1f} {py:.1f} L{b[0]:.1f} {b[1]:.1f}"/>'
    l += '<polygon points="' + " ".join(f"{x:.1f},{y:.1f}" for x, y in inner) + '"/>'
    return f'<g stroke-opacity=".75" stroke-width=".8">{l}</g>'

def face_mesh(cid):
    cx, cy, rx, ry, rot = EYE
    return ('<g fill="none" stroke-linecap="round" stroke-linejoin="round">' + eye_clip(cid) + eye_shape(.12) +
            hexgrid(cx, cy, rx + 3, ry + 3, 2.9, cid) + mirror(hexgrid(cx, cy, rx + 3, ry + 3, 2.9, cid)) +
            head_parts() + '</g>')

def face_lowpoly(cid):
    cx, cy, rx, ry, rot = EYE
    return ('<g fill="none" stroke-linecap="round" stroke-linejoin="round">' + eye_shape(.1) +
            lowpoly(cx, cy, rx, ry, rot) + mirror(lowpoly(cx, cy, rx, ry, rot)) + head_parts() + '</g>')

def face_glossy(cid):
    hl = '<path d="M20 30 C23 23 30 19 37 19" stroke-width="2.4" stroke-opacity=".9"/><circle cx="18.5" cy="37" r="1.6" fill="' + G + '"/>'
    return ('<g fill="none" stroke-linecap="round" stroke-linejoin="round">' + eye_shape(.22) + hl + mirror(hl) +
            head_parts(arista=False) + '</g>')

def eyes_only(cid):
    cx, cy, rx, ry, rot = EYE
    return ('<g fill="none" stroke-linecap="round">' + eye_clip(cid) + eye_shape(.16) +
            hexgrid(cx, cy, rx + 3, ry + 3, 3.6, cid) + mirror(hexgrid(cx, cy, rx + 3, ry + 3, 3.6, cid)) +
            '<path d="M47.5 66 L50 74 L52.5 66" stroke-opacity=".8"/></g>')

def hands(cid):
    legs = ('<path d="M33 64 C26 70 24 78 30 84 C35 89 41 91 46 92"/>'
            '<path d="M46 92 L49 89 M46 92 L49 95" stroke-opacity=".75"/>')
    return face_mesh(cid).replace('</g>', '', 0) + '<g fill="none" stroke-linecap="round" stroke-linejoin="round">' + legs + mirror(legs) + '</g>'

def badge(cid):
    return ('<g fill="none"><circle cx="50" cy="50" r="47"/><circle cx="50" cy="50" r="43" stroke-opacity=".35"/></g>'
            '<g transform="translate(13 12) scale(.74)">' + face_mesh(cid) + '</g>')

def solid():
    cx, cy, rx, ry, rot = EYE
    e = f'<ellipse cx="{cx}" cy="{cy}" rx="{rx}" ry="{ry}" transform="rotate({rot} {cx} {cy})"/>'
    return (f'<g fill="{G}" stroke="none">{e}{mirror(e)}'
            '<path d="M35 64 C42 72 58 72 65 64 C62 74 56 78 50 78 C44 78 38 74 35 64 Z"/>'
            '<rect x="46.5" y="76" width="7" height="10" rx="2"/><ellipse cx="50" cy="88" rx="10" ry="5.5"/></g>')

concepts = [
    ("F1", "Face · mesh", "hex-faceted compound eyes, antennae, sponge mouth", face_mesh),
    ("F2", "Face · low-poly", "faceted triangles instead of hexes", face_lowpoly),
    ("F3", "Face · glossy", "no mesh: glassy eyes with a highlight — friendlier", face_glossy),
    ("F4", "Eyes only", "the two eyes and a hint of mouth — most minimal", eyes_only),
    ("F5", "Rubbing its hands", "the classic fly gesture — a bit of character", hands),
    ("F6", "Badge", "the mesh face in a ring: app icon, dock, avatar", badge),
]

def svg(inner, size, sw, glow=True, col=G):
    f = 'filter="url(#glow)"' if glow else ''
    return (f'<svg width="{size}" height="{size}" viewBox="0 0 100 100"><defs><filter id="glow" x="-30%" y="-30%" width="160%" height="160%">'
            f'<feGaussianBlur stdDeviation="1.8" result="b"/><feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter></defs>'
            f'<g stroke="{col}" stroke-width="{sw}" {f}>{inner}</g></svg>')

cards = ""
for k, name, desc, fn in concepts:
    big = svg(fn(f"{k}a"), 230, 1.3)
    sizes = (f'<span>{svg(fn(f"{k}b"), 64, 1.9)}</span>'
             f'<span>{svg(fn(f"{k}c"), 32, 2.6)}</span>'
             f'<span>{svg(solid(), 16, 2, glow=False)}</span>'
             f'<span class="tb">{svg(fn(f"{k}e"), 20, 3, glow=False)}<b>FLY</b></span>'
             f'<span class="lt">{svg(fn(f"{k}f").replace(G, "#0a7a43"), 34, 2.2, glow=False, col="#0a7a43")}</span>')
    cards += (f'<div class="card"><div class="hd"><span class="k">{k}</span><span class="nm">{name}</span><span class="ds">{desc}</span></div>'
              f'<div class="big">{big}</div><div class="sizes">{sizes}</div></div>')

html = open("src/31-flies.html").read()
head = html[:html.index('<div class="top">')]
html = head + f'''<div class="top"><h1>Fly · face logo concepts</h1><span>front view: two huge compound eyes, antennae in the groove, sponging mouthparts · 16 px column uses the solid face</span></div>
<div class="grid">{cards}</div>'''
open("src/32-faces.html", "w").write(html)

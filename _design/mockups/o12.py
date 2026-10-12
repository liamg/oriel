# o12.py: refinements of the chosen O12 mark (o + block cursor) → src/35-o12.html
G, C = "#39ff88", "#5cd2ff"

def o_ring(cx=40, cy=54, r=22, sw=8, col=G):
    return f'<circle cx="{cx}" cy="{cy}" r="{r}" fill="none" stroke="{col}" stroke-width="{sw}"/>'

variants = [
    ("R1", "Original", "o and cursor share the same top and bottom", o_ring(38, 53, 21) + f'<rect x="68" y="28" width="13" height="50" fill="{G}"/>'),
    ("R2", "Block at x-height", "the cursor matches the o's height, like vim's normal-mode block", o_ring(36, 50, 23) + f'<rect x="67" y="27" width="20" height="46" rx="2" fill="{G}"/>'),
    ("R3", "Two-tone", "green o, cyan cursor: brand green and UI cyan, both in the mark", o_ring(36, 50, 23) + f'<rect x="67" y="27" width="20" height="46" rx="2" fill="{C}"/>'),
    ("R4", "Inverted cursor", "the cursor sits over a second o, as vim draws a block over a character", o_ring(30, 50, 19, 7) + f'<rect x="54" y="25" width="38" height="50" rx="3" fill="{G}"/>' + o_ring(73, 50, 13, 6, "#0b0f15")),
    ("R5", "Ghost cursor", "a hollow block cursor: quieter, and clearly not an “I”", o_ring(36, 50, 23) + f'<rect x="68" y="28" width="18" height="44" rx="2" fill="{G}" fill-opacity=".18" stroke="{G}" stroke-width="4"/>'),
    ("R6", "App icon", "R3 on a rounded dark tile, for the dock", f'<rect x="4" y="4" width="92" height="92" rx="22" fill="#0e1513" stroke="#1e2a24" stroke-width="1.5"/>' + o_ring(40, 50, 17, 6.5) + f'<rect x="64" y="32" width="15" height="36" rx="2" fill="{C}"/>'),
]

def svg(inner, size, glow=True):
    f = 'filter="url(#glow)"' if glow else ''
    return (f'<svg width="{size}" height="{size}" viewBox="0 0 100 100"><defs><filter id="glow" x="-30%" y="-30%" width="160%" height="160%">'
            f'<feGaussianBlur stdDeviation="2" result="b"/><feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter></defs>'
            f'<g {f}>{inner}</g></svg>')

cards = ""
for k, name, desc, inner in variants:
    light = inner.replace(G, "#0a7a43").replace(C, "#1f6fa8").replace("#0b0f15", "#eef2ee")
    cards += (f'<div class="card"><div class="hd"><span class="k">{k}</span><span class="nm">{name}</span></div><div class="ds">{desc}</div>'
              f'<div class="big">{svg(inner, 200)}</div>'
              f'<div class="sizes"><span>{svg(inner, 64)}</span><span>{svg(inner, 32, False)}</span><span>{svg(inner, 16, False)}</span></div>'
              f'<div class="ctx"><span class="tb">{svg(inner, 20, False)}<b>ORIEL</b></span><span class="lt">{svg(light, 30, False)}<i>oriel</i></span></div></div>')

html = f'''<!doctype html><meta charset="utf-8"><link rel="stylesheet" href="base.css">
<style>
html,body{{height:590px}}
body{{background:radial-gradient(1200px 600px at 50% 0%,#0d1712,var(--bg0))}}
.top{{padding:20px 30px 2px;display:flex;align-items:baseline;gap:14px}}
.top h1{{font:600 22px var(--disp);color:var(--fg)}}
.top span{{color:var(--fg3);font-size:13px}}
.grid{{display:grid;grid-template-columns:repeat(6,1fr);gap:14px;padding:12px 30px}}
.card{{background:rgba(11,15,21,.8);border:1px solid var(--line);border-radius:10px;overflow:hidden}}
.hd{{display:flex;align-items:baseline;gap:8px;padding:9px 12px 2px}}
.hd .k{{font:700 11px var(--mono);color:{G}}}
.hd .nm{{font:600 14px var(--disp);color:var(--fg)}}
.ds{{font-size:11px;color:var(--fg3);padding:0 12px 8px;height:44px;border-bottom:1px solid var(--line)}}
.big{{height:240px;display:grid;place-items:center;background:radial-gradient(circle,rgba(57,255,136,.06),transparent 60%)}}
.sizes{{display:flex;align-items:center;justify-content:center;gap:16px;padding:12px;border-top:1px solid var(--line)}}
.sizes svg{{display:block}}
.ctx{{display:flex;flex-direction:column;gap:8px;align-items:center;padding:10px 12px 14px;border-top:1px solid var(--line)}}
.tb{{display:flex;align-items:center;gap:6px;background:var(--bg1);border:1px solid var(--line2);border-radius:5px;padding:4px 9px}}
.tb b{{font:700 10px var(--disp);letter-spacing:.2em;color:var(--fg)}}
.lt{{display:flex;align-items:center;gap:6px;background:#eef2ee;border-radius:5px;padding:4px 10px}}
.lt i{{font:600 18px var(--disp);font-style:normal;color:#0d1712}}
</style>
<div class="top" data-h="590"><h1>Oriel · O12 refinements</h1><span>fixing the "oI" read at small sizes · R3/R6 also answer the accent question: green brand, cyan UI</span></div>
<div class="grid">{cards}</div>'''
open("src/35-o12.html", "w").write(html)

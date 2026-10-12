# finalists.py: logo finalists in context → src/33-finalists.html
from flies import tiny, G
from faces import face_lowpoly, badge, face_mesh, solid

def badge_lowpoly(cid):
    return ('<g fill="none"><circle cx="50" cy="50" r="47"/><circle cx="50" cy="50" r="43" stroke-opacity=".35"/></g>'
            '<g transform="translate(13 12) scale(.74)">' + face_lowpoly(cid) + '</g>')

cands = [
    ("6", "Solid fly", "filled top-down house fly", lambda c: tiny(), lambda: tiny()),
    ("F6", "Badge · mesh face", "hex-eyed face in a ring", badge, solid),
    ("F2", "Low-poly face", "triangulated compound eyes", face_lowpoly, solid),
    ("F2+", "Low-poly badge", "F2 in F6's ring", badge_lowpoly, solid),
]

def svg(inner, size, sw, glow=True, col=G):
    f = 'filter="url(#glow)"' if glow else ''
    return (f'<svg width="{size}" height="{size}" viewBox="0 0 100 100"><defs><filter id="glow" x="-30%" y="-30%" width="160%" height="160%">'
            f'<feGaussianBlur stdDeviation="1.8" result="b"/><feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter></defs>'
            f'<g stroke="{col}" stroke-width="{sw}" {f}>{inner}</g></svg>')

cols = ""
for k, name, desc, fn, small in cands:
    cols += f'''<div class="col">
  <div class="hd"><span class="k">{k}</span><span class="nm">{name}</span><span class="ds">{desc}</span></div>
  <div class="hero">{svg(fn(k+"a"), 200, 1.3)}</div>
  <div class="row2">
    <div class="appicon">{svg(fn(k+"b"), 76, 1.9)}</div>
    <div class="fav"><div class="tabchrome"><span>{svg(small(), 16, 2, glow=False)}</span>Fly — pricing-rounding</div>
      <div class="sz">{svg(fn(k+"c"), 32, 2.6)}{svg(small(), 16, 2, glow=False)}</div></div>
  </div>
  <div class="titlebar2"><span class="b2">{svg(fn(k+"d"), 20, 3, glow=False)}<b>FLY</b></span><span class="st"><i></i>pricing-rounding</span><span class="st"><i class="r"></i>INF-2231</span></div>
  <div class="lockup">{svg(fn(k+"e"), 54, 2)}<span>fly</span></div>
  <div class="light">{svg(fn(k+"f").replace(G,"#0a7a43"), 44, 2.2, glow=False, col="#0a7a43")}<span>fly</span></div>
</div>'''

html = open("src/31-flies.html").read()
head = html[:html.index('<style>')]
html = head + f'''<style>
html,body{{height:700px}}
body{{background:radial-gradient(1200px 600px at 50% 0%,#0d1712,var(--bg0))}}
.top{{padding:22px 34px 4px;display:flex;align-items:baseline;gap:14px}}
.top h1{{font:600 22px var(--disp);color:var(--fg)}}
.top span{{color:var(--fg3);font-size:13px}}
.grid{{display:grid;grid-template-columns:repeat(4,1fr);gap:18px;padding:14px 34px}}
.col{{background:rgba(11,15,21,.8);border:1px solid var(--line);border-radius:10px;overflow:hidden;display:flex;flex-direction:column}}
.hd{{display:flex;align-items:baseline;gap:8px;padding:10px 14px;border-bottom:1px solid var(--line)}}
.hd .k{{font:700 12px var(--mono);color:{G}}}
.hd .nm{{font:600 14px var(--disp);color:var(--fg);white-space:nowrap}}
.hd .ds{{font-size:11px;color:var(--fg3);margin-left:auto;text-align:right}}
.hero{{height:250px;display:grid;place-items:center;background:radial-gradient(circle,rgba(57,255,136,.07),transparent 60%)}}
.row2{{display:flex;gap:14px;align-items:center;padding:12px 14px;border-top:1px solid var(--line)}}
.appicon{{width:104px;height:104px;border-radius:24px;background:linear-gradient(160deg,#141c18,#070a09);border:1px solid #1e2a24;display:grid;place-items:center;box-shadow:0 10px 30px rgba(0,0,0,.6),inset 0 1px rgba(255,255,255,.05)}}
.fav{{flex:1;display:flex;flex-direction:column;gap:10px}}
.tabchrome{{display:flex;align-items:center;gap:7px;background:var(--bg2);border:1px solid var(--line2);border-radius:7px 7px 0 0;padding:6px 9px;font-size:11px;color:var(--fg2);white-space:nowrap;overflow:hidden}}
.sz{{display:flex;gap:12px;align-items:center}}
.titlebar2{{display:flex;align-items:stretch;height:34px;border-top:1px solid var(--line);border-bottom:1px solid var(--line);background:var(--bg1)}}
.b2{{display:flex;align-items:center;gap:7px;padding:0 12px;border-right:1px solid var(--line)}}
.b2 b{{font:700 11px var(--disp);letter-spacing:.2em;color:var(--fg)}}
.st{{display:flex;align-items:center;gap:6px;padding:0 10px;font-size:11.5px;color:var(--fg2);border-right:1px solid var(--line)}}
.st i{{width:6px;height:6px;border-radius:50%;background:var(--orange);box-shadow:0 0 6px var(--orange)}}
.st i.r{{background:var(--red);box-shadow:0 0 6px var(--red)}}
.lockup{{display:flex;align-items:center;justify-content:center;gap:12px;padding:16px;}}
.lockup span{{font:600 40px var(--disp);letter-spacing:.02em;color:var(--fg)}}
.light{{display:flex;align-items:center;justify-content:center;gap:10px;padding:12px;background:#eef2ee;margin-top:auto}}
.light span{{font:600 30px var(--disp);color:#0d1712}}
</style>
<div class="top" data-h="700"><h1>Fly · finalists in context</h1><span>app icon · browser tab + 32/16 px · titlebar · wordmark lockup · on light</span></div>
<div class="grid">{cols}</div>'''
open("src/33-finalists.html", "w").write(html)

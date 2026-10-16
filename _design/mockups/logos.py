import math
import os
G="#39ff88"
def mirror(inner): return f'<g transform="translate(100,0) scale(-1,1)">{inner}</g>'
def lines(pairs): return "".join(f'<line x1="{a[0]}" y1="{a[1]}" x2="{b[0]}" y2="{b[1]}"/>' for a,b in pairs)
def poly(pts,extra=""): return '<polygon points="'+" ".join(f"{x},{y}" for x,y in pts)+f'" {extra}/>'

# 1 mesh
wingL=[(44,38),(28,40),(13,52),(10,66),(18,76),(32,70),(43,48)]
wingV=[((44,38),(18,76)),((28,40),(32,70)),((13,52),(32,70)),((28,40),(10,66)),((43,48),(18,76))]
mesh=('<g fill="none">'+poly(wingL)+lines(wingV)+mirror(poly(wingL)+lines(wingV))+
 poly([(50,16),(56,20),(56,27),(50,31),(44,27),(44,20)])+lines([((44,20),(56,27)),((56,20),(44,27))])+
 poly([(50,31),(58,37),(57,47),(50,51),(43,47),(42,37)])+lines([((50,31),(50,51)),((42,37),(57,47)),((58,37),(43,47))])+
 poly([(50,51),(56,57),(55,71),(50,84),(45,71),(44,57)])+lines([((44,57),(56,57)),((45,64),(55,64)),((45,71),(55,71)),((50,51),(50,84))])+
 '</g>')

# 2 compound eye: hex grid in circle
hexes=[]
r=6.2
for row in range(-8,9):
  for col in range(-8,9):
    cx=50+col*r*math.sqrt(3)+(row%2)*r*math.sqrt(3)/2; cy=50+row*r*1.5
    d=math.hypot(cx-50,cy-50)
    if d<40:
      pts=[(cx+(r-0.6)*math.cos(math.radians(60*k+30)),cy+(r-0.6)*math.sin(math.radians(60*k+30))) for k in range(6)]
      op=max(0.18,1-d/44)
      hexes.append(poly([(round(x,2),round(y,2)) for x,y in pts],f'fill="{G}" fill-opacity="{op*0.35:.2f}" stroke-opacity="{op:.2f}"'))
eye='<g>'+"".join(hexes)+'<circle cx="50" cy="50" r="42" fill="none"/></g>'

# 3 monoline
mono=('<g fill="none" stroke-linecap="round" stroke-linejoin="round">'
 '<path d="M50 22 a6 6 0 1 1 -0.01 0 Z"/>'
 '<path d="M50 34 C58 34 60 44 58 54 C56 68 53 80 50 86 C47 80 44 68 42 54 C40 44 42 34 50 34 Z"/>'
 '<path d="M45 40 C34 36 16 44 12 58 C9 70 22 74 32 66 C40 60 44 50 45 44"/>'
 '<path d="M55 40 C66 36 84 44 88 58 C91 70 78 74 68 66 C60 60 56 50 55 44"/>'
 '<path d="M44 58 L56 58 M45 67 L55 67"/></g>')

# 4 diamond body
dia=('<g fill="none">'+poly([(50,26),(62,50),(50,82),(38,50)])+lines([((38,50),(62,50)),((50,26),(50,82))])+
 '<circle cx="50" cy="18" r="5"/>'+
 poly([(41,44),(20,36),(8,46),(14,58),(36,54)])+lines([((41,44),(8,46)),((20,36),(14,58)),((36,54),(8,46))])+
 mirror(poly([(41,44),(20,36),(8,46),(14,58),(36,54)])+lines([((41,44),(8,46)),((20,36),(14,58)),((36,54),(8,46))]))+'</g>')

# 5 F-wing
fw=('<g fill="none" stroke-linejoin="round">'
 +poly([(30,16),(40,16),(40,86),(30,86)])+
 poly([(40,18),(84,10),(78,24),(40,30)])+lines([((40,24),(84,10)),((52,17),(50,28)),((64,14),(62,26))])+
 poly([(40,44),(72,38),(66,52),(40,56)])+lines([((40,50),(72,38)),((52,42),(50,54))])+
 '</g>')

# 6 pixel
P=["....X.X....",
   ".....X.....",
   "....XXX....",
   "XXX.XXX.XXX",
   ".XXXXXXXXX.",
   "..XX.X.XX..",
   ".XX..X..XX.",
   ".....X.....",
   "....XXX....",
   "....XXX....",
   ".....X....."]
px=""
for y,row in enumerate(P):
  for x,c in enumerate(row):
    if c=="X": px+=f'<rect x="{10+x*7.3:.1f}" y="{10+y*7.3:.1f}" width="6.3" height="6.3" fill="{G}" stroke="none"/>'
pix='<g>'+px+'</g>'

# 7 single wing venation
wing=('<g fill="none" stroke-linejoin="round" transform="rotate(-28 50 50)">'
 '<path d="M14 54 C26 34 60 24 86 34 C92 38 90 50 82 56 C64 68 32 70 14 54 Z"/>'
 '<path d="M14 54 C38 46 62 40 86 38"/><path d="M14 54 C40 54 62 52 84 50"/><path d="M30 60 C44 50 54 40 60 28"/>'
 '<path d="M50 64 C56 54 64 46 72 30"/><path d="M68 60 C72 52 78 44 82 36"/>'
 '<circle cx="14" cy="54" r="3" fill="'+G+'"/></g>')

# 8 orbit
orb=('<g fill="none">'
 '<ellipse cx="50" cy="50" rx="44" ry="16" transform="rotate(-20 50 50)" stroke-dasharray="2 4" stroke-opacity=".6"/>'
 '<circle cx="50" cy="36" r="5"/><ellipse cx="50" cy="48" rx="6" ry="7"/><ellipse cx="50" cy="64" rx="5" ry="10"/>'
 '<path d="M45 44 C30 30 18 36 22 48 C25 56 38 54 45 48"/><path d="M55 44 C70 30 82 36 78 48 C75 56 62 54 55 48"/>'
 '<circle cx="91" cy="35" r="2.5" fill="'+G+'"/></g>')

def hexp(cx,cy,r): return [(round(cx+r*math.cos(math.radians(60*k)),2),round(cy+r*math.sin(math.radians(60*k)),2)) for k in range(6)]
eyes=poly(hexp(42,24,7))+poly(hexp(58,24,7))+lines([((35,24),(49,24)),((51,24),(65,24)),((38.5,18),(45.5,30)),((54.5,18),(61.5,30))])
meshEyes=mesh.replace(poly([(50,16),(56,20),(56,27),(50,31),(44,27),(44,20)])+lines([((44,20),(56,27)),((56,20),(44,27))]),eyes)
flight='<g transform="rotate(-24 50 50) translate(4,2) scale(.92)">'+mesh+'</g>'+'<g fill="none" stroke-opacity=".5" stroke-linecap="round">'+lines([((70,86),(90,92)),((76,78),(94,82)),((64,92),(80,98))])+'</g>'
concepts=[("A","Mesh","low-poly wireframe, top-down",mesh),
 ("A2","Mesh · big eyes","house-fly head: two faceted eyes",meshEyes),
 ("A3","Mesh · in flight","angled, with motion lines",flight),
 ("B","Compound eye","honeycomb facets, no fly at all — abstract",eye),
 ("C","Neon tube","one weight of monoline, like a neon sign",mono),
 ("D","Diamond fly","keeps the current diamond as the body",dia),
 ("E","F-wing","the letter F; bars become swept wings",fw),
 ("F","Pixel","11×11 sprite — a nod to the terminal editor",pix),
 ("G","Wing","one wing's vein mesh",wing),
 ("H","Orbit","fly plus its flight path",orb)]

def svg(inner,size,glow=True,sw=1.6,col=G):
  f=f'filter="url(#glow)"' if glow else ''
  return (f'<svg width="{size}" height="{size}" viewBox="0 0 100 100"><defs><filter id="glow" x="-30%" y="-30%" width="160%" height="160%">'
   f'<feGaussianBlur stdDeviation="2.2" result="b"/><feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter></defs>'
   f'<g stroke="{col}" stroke-width="{sw}" {f}>{inner}</g></svg>')

cards=""
for k,name,desc,inner in concepts:
  small_sw={64:2.2,32:3.2,16:4.5}
  cards+=f'''<div class="card brk"><div class="hd"><span class="k">{k}</span><span class="nm">{name}</span><span class="ds">{desc}</span></div>
  <div class="big">{svg(inner,170)}</div>
  <div class="sizes"><span>{svg(inner,64,sw=small_sw[64])}</span><span>{svg(inner,32,sw=small_sw[32])}</span><span>{svg(inner,16,glow=False,sw=small_sw[16])}</span>
  <span class="tb">{svg(inner,18,glow=False,sw=4)}<b>FLY</b></span><span class="lt">{svg(inner,30,glow=False,sw=2.6,col="#0a7a43")}</span></div></div>'''
html=f'''<!doctype html><meta charset="utf-8"><link rel="stylesheet" href="base.css">
<style>
html,body{{height:930px}}
body{{background:radial-gradient(1200px 600px at 50% 0%,#0d1712,var(--bg0))}}
.top{{padding:26px 34px 6px;display:flex;align-items:baseline;gap:14px}}
.top h1{{font:600 22px var(--disp);color:var(--fg)}}
.top span{{color:var(--fg3);font-size:13px}}
.grid{{display:grid;grid-template-columns:repeat(5,1fr);gap:18px;padding:16px 34px 30px}}
.card{{background:rgba(11,15,21,.8);border:1px solid var(--line);border-radius:10px;overflow:hidden;--cyan:#39ff88}}
.hd{{display:flex;align-items:flex-start;gap:8px;padding:10px 14px;height:52px;border-bottom:1px solid var(--line)}}
.hd .k{{font:700 12px var(--mono);color:#39ff88}}
.hd .nm{{font:600 14px var(--disp);color:var(--fg);white-space:nowrap}}
.hd .ds{{font-size:11px;color:var(--fg3);margin-left:auto;text-align:right}}
.big{{height:220px;display:grid;place-items:center;background:radial-gradient(circle at 50% 50%,rgba(57,255,136,.06),transparent 60%)}}
.sizes{{display:flex;align-items:center;gap:10px;padding:12px 14px;border-top:1px solid var(--line)}}
.sizes svg{{display:block}}
.tb{{display:flex;align-items:center;gap:7px;background:var(--bg1);border:1px solid var(--line2);border-radius:5px;padding:5px 9px;margin-left:auto}}
.tb b{{font:700 11px var(--disp);letter-spacing:.2em;color:var(--fg)}}
.lt{{background:#eef2ee;border-radius:5px;padding:4px;display:grid;place-items:center}}
</style>
<div class="top" data-h="930"><h1>Fly · logo concepts</h1><span>neon #39ff88 on graphite · each at 170 · 64 · 32 · 16 px, in the titlebar, and on light</span></div>
<div class="grid">{cards}</div>'''
open(os.path.join(os.path.dirname(os.path.abspath(__file__)), 'src', '30-logos.html'), 'w').write(html)

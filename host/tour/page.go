package tour

import (
	"maps"
	"slices"
	"strings"
)

// Colors is one theme for the export: its CSS variables (bg, bg-raised, bg-lift, bg-sunken, fg, fg-muted, fg-faint,
// border, border-strong, accent, accent-fg, add-fg, del-fg, agent; without their --) and its code classes'
// declarations, by class.
type Colors struct {
	Vars map[string]string
	Code map[string]string
}

// Palette is the page's colours for a dark and a light theme. The page follows the reader's preference, and a host
// that stamps data-theme="dark" or "light" on the root switches it, winning over the preference either way.
func Palette(dark, light Colors) string {
	var b strings.Builder
	vars := func(c Colors) string {
		var s strings.Builder
		for _, k := range slices.Sorted(maps.Keys(c.Vars)) {
			s.WriteString("  --" + k + ": " + c.Vars[k] + ";\n")
		}
		return s.String()
	}
	code := func(c Colors, root string) string {
		var s strings.Builder
		for _, k := range slices.Sorted(maps.Keys(c.Code)) {
			s.WriteString(root + " ." + k + " { " + c.Code[k] + " }\n")
		}
		return s.String()
	}
	b.WriteString(":root {\n" + vars(dark) + "}\n")
	b.WriteString(code(dark, ":root"))
	b.WriteString("@media (prefers-color-scheme: light) {\n:root {\n" + vars(light) + "}\n" + code(light, ":root") + "}\n")
	b.WriteString(`:root[data-theme="dark"] {` + "\n" + vars(dark) + "}\n")
	b.WriteString(code(dark, `:root[data-theme="dark"]`))
	b.WriteString(`:root[data-theme="light"] {` + "\n" + vars(light) + "}\n")
	b.WriteString(code(light, `:root[data-theme="light"]`))
	return b.String()
}

// DefaultPalette is the page's colours when its caller has none of its own: plain code, Oriel's default dark and
// light.
func DefaultPalette() string {
	return Palette(Colors{Vars: map[string]string{
		"bg": "#0b0b0c", "bg-raised": "#111113", "bg-lift": "#18181b", "bg-sunken": "#070708", "fg": "#e8e6e1", "fg-muted": "#b4b1aa",
		"fg-faint": "#7d7a74", "border": "#232326", "border-strong": "#34343a", "accent": "#f5c518", "accent-fg": "#0b0b0c",
		"add-fg": "#7bd88f", "del-fg": "#ff6b6b", "agent": "#b392f0"}}, Colors{Vars: map[string]string{
		"bg": "#fbfaf7", "bg-raised": "#ffffff", "bg-lift": "#f1efe9", "bg-sunken": "#ebe8e1", "fg": "#1d1c1a", "fg-muted": "#4a4843",
		"fg-faint": "#86827a", "border": "#e2ded5", "border-strong": "#cfcac0", "accent": "#a16d00", "accent-fg": "#ffffff",
		"add-fg": "#2f7d3c", "del-fg": "#b42318", "agent": "#6d4bc2"}})
}

// pageCSS is the page around the blocks and the blocks themselves, all in the palette's variables.
const pageCSS = `
:root { --sans: -apple-system, BlinkMacSystemFont, "Segoe UI", Inter, sans-serif; --mono: "JetBrains Mono", ui-monospace, Menlo, monospace;
  --radius: 4px; --code-size: 12.5px; --code-line: 1.6; }
* { box-sizing: border-box; }
body.tour-export { margin: 0; padding: 0 0 84px; background: var(--bg); color: var(--fg); font-family: var(--sans); font-size: 15px; line-height: 1.65; }
.tx-head { position: sticky; top: 0; z-index: 5; display: flex; align-items: baseline; gap: 12px; flex-wrap: wrap;
  padding: 12px 32px; border-bottom: 1px solid var(--border); background: var(--bg-raised); }
.tx-title { font-size: 14px; font-weight: 600; color: var(--fg-muted); }
.tx-meta { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; font-size: 11.5px; }
.tx-kind { padding: 1px 7px; border: 1px solid var(--border-strong); border-radius: 999px; color: var(--accent); text-transform: lowercase; }
.tx-repo { color: var(--fg-muted); }
.tx-sha { color: var(--fg-faint); font-family: var(--mono); }
main#tx-steps { max-width: 900px; margin: 0 auto; padding: min(7vh, 64px) 32px 40px; }
.tx-step { display: none; }
.tx-step.on { display: block; }
.tx-stepnum { color: var(--accent); font: 11px var(--mono); letter-spacing: .18em; text-transform: uppercase; }
.tx-steptitle { margin: 8px 0 26px; font-size: 30px; line-height: 1.18; letter-spacing: -.01em; }
.tx-step .tour-prose > p:first-child { font-size: 16.5px; }
.tx-nav { position: fixed; left: 0; right: 0; bottom: 0; display: flex; align-items: center; gap: 14px; padding: 10px 28px;
  border-top: 1px solid var(--border); background: var(--bg-raised); }
.tx-btn { padding: 5px 14px; border: 1px solid var(--border-strong); border-radius: var(--radius); background: var(--bg-lift);
  color: var(--fg); font: inherit; font-size: 12.5px; cursor: pointer; }
.tx-btn:hover:not(:disabled) { border-color: var(--accent); color: var(--accent); }
.tx-btn:disabled { opacity: .35; cursor: default; }
.tx-btn.plain { margin-left: auto; background: none; border-color: var(--border); }
.tx-dots { display: flex; gap: 6px; flex: 1; justify-content: center; flex-wrap: wrap; }
.tx-dot { width: 9px; height: 9px; padding: 0; border: 0; border-radius: 50%; background: var(--border-strong); cursor: pointer; }
.tx-dot.on { background: var(--accent); }
body.presenting main#tx-steps { max-width: 1100px; padding: 7vh 6vw; font-size: 17px; }
body.presenting .tx-head { display: none; }
body.presenting .tx-steptitle { font-size: 40px; margin-bottom: 32px; }
body.presenting .tour-prose { font-size: 18px; }
body.presenting .tq-code { font-size: 15px; }
@media (max-width: 720px) {
  main#tx-steps { padding: 20px 14px 32px; }
  .tx-head, .tx-nav { padding: 10px 14px; }
  .tx-steptitle { font-size: 23px; }
}
.tour-prose { max-width: 78ch; }
.tour-prose h2, .tour-prose h3, .tour-prose h4 { margin: 22px 0 8px; line-height: 1.3; }
.tour-prose p { margin: 0 0 12px; }
.tour-prose ul, .tour-prose ol { margin: 0 0 12px; padding-left: 22px; }
.tour-prose code, .tq-note code, .tour-aside code { background: var(--bg-lift); border-radius: var(--radius); padding: 0 4px; font-family: var(--mono); font-size: .9em; }
.tour-prose blockquote { margin: 0 0 12px; padding: 6px 12px; border-left: 2px solid var(--border-strong); color: var(--fg-muted); }
.tour-prose a { color: var(--accent); }
.tour-img { max-width: 100%; height: auto; display: block; margin: 14px 0; border: 1px solid var(--border); border-radius: var(--radius); }
.tour-prose .tblwrap { overflow-x: auto; margin: 0 0 14px; }
.tour-prose table { border-collapse: collapse; font-size: 12px; }
.tour-prose th, .tour-prose td { border: 1px solid var(--border); padding: 4px 9px; text-align: left; }
.tour-prose th { background: var(--bg-lift); }
.tour-quote, .tour-example, .tour-run, .tour-diagram { margin: 22px 0; border: 1px solid var(--border); border-radius: var(--radius);
  background: var(--bg-raised); overflow: hidden; }
.tour-quote figcaption, .tour-example figcaption, .tour-run figcaption { display: flex; align-items: center; gap: 8px; padding: 6px 12px;
  font: 11.5px var(--mono); border-bottom: 1px solid var(--border); color: var(--fg-muted); }
.tq-repo { color: var(--accent); }
.tq-path { color: var(--fg); }
.tq-sep, .tq-lines, .tq-tag { color: var(--fg-faint); }
.tq-lang { color: var(--fg-faint); text-transform: uppercase; border: 1px solid var(--border-strong); border-radius: 999px; padding: 0 7px; font-size: 10px; }
.tq-grow { flex: 1; }
.tq-rev { color: var(--agent); border: 1px solid var(--border-strong); border-radius: var(--radius); padding: 0 5px; }
.tq-code { padding: 6px 0; overflow-x: auto; font-family: var(--mono); font-size: var(--code-size); color: var(--fg); }
.tq-line { display: flex; line-height: var(--code-line); white-space: pre; }
.tq-line code { flex: 1; padding: 0 14px 0 12px; white-space: pre; font-family: inherit; }
.tq-n { flex: 0 0 auto; min-width: 5ch; padding: 0 0 0 10px; text-align: right; color: var(--fg-faint); user-select: none; }
.tq-line.marked { background: var(--bg-lift); box-shadow: inset 2px 0 0 var(--accent); }
.tq-line.marked .tq-n { color: var(--accent); }
.tq-code.collapsed .tq-hidden { display: none; }
.tq-more { display: block; width: 100%; margin-top: 4px; padding: 4px 0; background: none; border: 0; border-top: 1px dashed var(--border);
  color: var(--fg-faint); font: inherit; font-size: 11.5px; cursor: pointer; }
.tq-more:hover { color: var(--accent); }
.tq-clip, .tq-missing { padding: 8px 12px; color: var(--fg-muted); font-size: 12px; }
.tour-quote.missing { border-style: dashed; }
.tour-quote.missing .tq-missing { color: var(--del-fg); }
.tq-note { display: flex; gap: 10px; margin: 6px 14px 8px 6ch; padding: 8px 12px; border: 1px solid var(--border-strong); border-radius: var(--radius);
  background: var(--bg-lift); font: 13px/1.55 var(--sans); white-space: normal; }
.tn-n { flex: 0 0 18px; height: 18px; display: grid; place-items: center; border-radius: 50%; background: var(--agent); color: var(--bg);
  font: 600 10px var(--mono); margin-top: 2px; }
.tn-body p { margin: 0 0 6px; }
.tn-body p:last-child { margin: 0; }
.tour-aside { margin: 14px 0; padding: 8px 12px; border-left: 2px solid var(--border-strong); color: var(--fg-muted); }
.tour-example .tq-code { padding: 8px 0 8px 0; }
.tour-run figcaption { font-family: var(--mono); }
.tr-prompt { color: var(--add-fg); }
.tr-cmd { color: var(--fg); }
.tr-cwd, .tr-meta { color: var(--fg-faint); }
.tr-exit { color: var(--del-fg); }
.tr-out { margin: 0; padding: 8px 12px; overflow-x: auto; font-family: var(--mono); font-size: var(--code-size); line-height: var(--code-line);
  color: var(--fg-muted); white-space: pre-wrap; word-break: break-word; }
.tour-shot { border: 0; background: none; margin: 22px 0; }
.ts-wrap { position: relative; display: block; line-height: 0; border: 1px solid var(--border); border-radius: var(--radius); overflow: hidden;
  background: var(--bg-sunken); }
.ts-wrap img { display: block; width: 100%; height: auto; }
.ts-arrows { position: absolute; inset: 0; width: 100%; height: 100%; z-index: 2; stroke: var(--accent); fill: var(--accent); stroke-linecap: round;
  filter: drop-shadow(0 1px 2px rgba(0, 0, 0, .55)); }
.ts-mark { position: absolute; z-index: 3; display: flex; align-items: center; gap: 6px; transform: translate(-50%, -50%); line-height: 1.2; white-space: nowrap; }
.ts-mark.below { transform: translate(-50%, 6px); }
.ts-mark.left { flex-direction: row-reverse; transform: translate(-100%, -50%); }
.ts-mark.edge { transform: translate(0, -50%); }
.ts-mark.edge.below { transform: translate(0, 6px); }
.ts-n { display: grid; place-items: center; width: 17px; height: 17px; flex: 0 0 17px; border-radius: 50%; background: var(--accent);
  color: var(--accent-fg); font: 600 10px var(--mono); box-shadow: 0 0 0 3px color-mix(in srgb, var(--bg) 70%, transparent); }
.ts-label { padding: 2px 8px; border-radius: var(--radius); background: color-mix(in srgb, var(--bg) 88%, var(--accent) 12%);
  border: 1.5px solid var(--accent); color: var(--fg); font: 600 11.5px/1.5 var(--sans); white-space: nowrap;
  box-shadow: 0 2px 10px -2px rgba(0, 0, 0, .75), 0 0 0 3px color-mix(in srgb, var(--bg) 62%, transparent); }
.ts-box { position: absolute; z-index: 1; border: 2px solid var(--accent); border-radius: var(--radius);
  box-shadow: 0 0 0 9999px color-mix(in srgb, var(--bg-sunken) 28%, transparent); }
.ts-box .ts-label { position: absolute; left: -2px; top: calc(100% + 7px); }
.ts-box.above .ts-label { top: auto; bottom: calc(100% + 7px); }
.ts-cap, .td-cap { margin-top: 8px; color: var(--fg-muted); font-size: 12px; text-align: center; }
.tour-shot.missing .ts-missing { padding: 10px 12px; border: 1px dashed var(--border-strong); border-radius: var(--radius); color: var(--del-fg); font-size: 12px; }
.tour-diagram { padding: 14px; }
.td-kind { color: var(--fg-faint); font: 10px var(--mono); letter-spacing: .14em; text-transform: uppercase; margin-bottom: 6px; }
.tour-mermaid pre.mermaid { margin: 0; font: 12px var(--mono); color: var(--fg-muted); white-space: pre-wrap; }
`

// pageJS walks the steps: ← → (and j, k, space), dots, P to present, #step=N in the address, and "… N more lines".
const pageJS = `(function () {
  var steps = Array.prototype.slice.call(document.querySelectorAll(".tx-step"));
  var dots = document.getElementById("tx-dots");
  var prev = document.getElementById("tx-prev");
  var next = document.getElementById("tx-next");
  var present = document.getElementById("tx-present");
  var at = 0;
  steps.forEach(function (_, i) {
    var d = document.createElement("button");
    d.className = "tx-dot" + (i === 0 ? " on" : "");
    d.type = "button";
    d.title = "Step " + (i + 1);
    d.addEventListener("click", function () { go(i); });
    dots.appendChild(d);
  });
  function go(i) {
    if (i < 0 || i >= steps.length) return;
    at = i;
    steps.forEach(function (s, n) { s.classList.toggle("on", n === i); });
    Array.prototype.forEach.call(dots.children, function (d, n) { d.classList.toggle("on", n === i); });
    prev.disabled = i === 0;
    next.disabled = i === steps.length - 1;
    history.replaceState(null, "", "#step=" + (i + 1));
    window.scrollTo(0, 0);
  }
  function togglePresent() {
    document.body.classList.toggle("presenting");
    if (document.body.classList.contains("presenting")) {
      if (document.documentElement.requestFullscreen) document.documentElement.requestFullscreen().catch(function () {});
    } else if (document.fullscreenElement && document.exitFullscreen) {
      document.exitFullscreen().catch(function () {});
    }
  }
  prev.addEventListener("click", function () { go(at - 1); });
  next.addEventListener("click", function () { go(at + 1); });
  present.addEventListener("click", togglePresent);
  document.addEventListener("keydown", function (e) {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    if (e.key === "ArrowRight" || e.key === "j" || e.key === " ") { go(at + 1); e.preventDefault(); }
    else if (e.key === "ArrowLeft" || e.key === "k") { go(at - 1); e.preventDefault(); }
    else if (e.key === "p") togglePresent();
    else if (e.key === "Escape" && document.body.classList.contains("presenting")) togglePresent();
  });
  document.addEventListener("click", function (e) {
    var b = e.target.closest ? e.target.closest(".tq-more") : null;
    if (!b) return;
    var code = b.closest(".tq-code");
    if (code) code.classList.remove("collapsed");
    b.remove();
  });
  var m = /#step=(\d+)/.exec(location.hash || "");
  go(m ? Math.min(steps.length, Math.max(1, parseInt(m[1], 10))) - 1 : 0);
})();
`

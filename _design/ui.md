# UI: the screens

Sources live in `mockups/src/*.html` (one shared `base.css` of theme tokens),
and `mockups/render.sh` turns them into the PNGs below.
They are design sketches, not the implementation: the app draws natively (see `shell.md`).

## Design language

- **Black, yellow and a little orange** (the default theme; any theme can change it). Warm near-black surfaces, a signal-yellow
  accent `#ffd23f` for focus and primary actions, and orange `#ff8a1f` used sparingly (the logo's cursor, debug mode, warnings).
  Colour means status, never decoration.
- **Status colours**, the same everywhere (tabs, cards, gutters, threads), chosen to stay clear of the brand colours:
  red = needs you · violet = wants permission · cyan = working · blue = CI running · pink = with a reviewer ·
  green = quiet · grey = off.
- **Sci-fi through restraint**: hairline frames, corner brackets, mono
  micro-labels in caps, faint grid backdrops, a soft glow on live things.
  No 3D, no skeuomorphism, no motion that isn't telling you something.
- **Type**: JetBrains Mono for code and readouts, Inter for UI and prose,
  Space Grotesk for display headings.
- **Tats**: tiny live readouts (label, bar or value, sparkline) placed in fixed
  slots: titlebar right, statusline, session footer, mission-control strip.
  Hold `⌥` to pull the full HUD down over anything.
- **Annotation house style** (explain, tours, `:Inspect`): numbered diamond,
  angled leader line, clipped-corner card.

## Logo: **o▌** (decided): a yellow o with an orange block cursor, in the default theme

![35-o12](mockups/35-o12.png)

- **R1 chosen**: the original proportions, all green. It's in every mockup's titlebar (`base.css` `.brand i`). Final pass in
  phase 1 (framework): per-size stroke tuning, a 16 px cut, and SVG/PNG/ICNS exports. Source: `mockups/o12.py`.


![34-o-logos](mockups/34-o-logos.png)

- Twelve O concepts in the same neon style. Source: `mockups/ologos.py`.
- The fly concepts below are kept for reference only.


- Chosen: F2, the low-poly face (see Finalists below). Still to do: a final pass with stroke weights tuned per size, a
  hand-tuned 16 px cut, and SVG/PNG/ICNS exports (phase 1, along with the framework). The mockups' titlebar
  diamond becomes F2.
- The concept rounds below are kept for reference.


![31-flies](mockups/31-flies.png)

- House-fly anatomy: huge compound eyes, a stout striped thorax, a short round abdomen, two clear wings folded in a V,
  and six bent legs. Neon `#39ff88` hairlines with a glow.
- 1–5 are line marks. 6 is the solid silhouette the line marks fall back to at 16 px (favicon, dock).
  Each is shown at 230, 64, 32 and 16 px, in the titlebar, and on light.
- The first round (`mockups/30-logos.png`) read as bees and wasps, and was dropped.
- Open question: keep cyan as the UI accent with green for the brand only, or go green throughout (a theme token change).
- Source: `mockups/flies.py`.

**Face variants**

![32-faces](mockups/32-faces.png)

- A front view: two huge compound eyes (mesh, low-poly or glossy), antennae in the facial groove, and the sponge-tipped mouthparts.
  F5 rubs its hands. F6 is the badge for the app icon.
- Source: `mockups/faces.py`.

**Finalists in context**

![33-finalists](mockups/33-finalists.png)

- 6, F6, F2, and F2 in F6's ring, each shown as app icon, browser tab, 32/16 px, titlebar, wordmark and on light. Source: `mockups/finalists.py`.

## 01 · The IDE

![01-ide](mockups/01-ide.png)

- Session tabs across the top, each with a status dot, kind and `⌘N`. The status reads at a glance.
- Threads open **between the lines** they belong to, with the agent's live state in the header.
- Agent-written lines carry a gutter mark and an "agent · 2m" ghost until you've read them.
- Right: a **summary** of requests (threads), the task rail, and the session tally (context, cache, cost, review ledger, tests).
  The full conversation view is 01b.
- Bottom: the agent pane, terminal, problems, debug and tests. The orange tab dot means something's running.
- Statusline: vim mode, branch, diagnostics, macro recording, worker CPU/mem tats.

## 01b · Requests: the main conversation view

![21-requests](mockups/21-requests.png)

- A main view at the same level as Explorer: rail icon, `g r` or `⌘⇧R`. The right panel in 01 is only its summary.
- Left: every request, grouped Working / Needs you / Open / Resolved, with a preview of the last reply.
  Filters and search. **New request** (`⌘N`) starts one that isn't anchored to a line.
- Centre: one request at a readable width.
  - The anchored code at the top, which follows edits.
  - Your long prompt with markdown, lists and pasted images.
  - The agent's reply rendered in full: prose, diffs, an activity block (edits, LSP lookups, test runs, each linking to the diff or log), and evidence chips.
- A large composer: `@file`, `#symbol`, image paste, dictation, **urgent** (interrupts the turn), **btw** (a side question answered by a fork).
- Right: what this request changed (with its own read/unread ledger), what it spawned (tasks, new requests), and related requests.
- Keyboard: `j`/`k` between requests, `⏎` to the composer, `o` jumps to the anchor in the editor, `R` resolves.
- Inline threads in the editor (01) and this view are two views of the same thread: reply in either.

## 01e · Start view: nothing open

![39-start](mockups/39-start.png)

![39b-start-empty](mockups/39b-start-empty.png)

What the window shows with no directory and no session open: at launch (plain `oriel`), and when the last one closes.

- Two columns. **Start**: New Session (`⌘N`), Resume a Session (`⌘O`), Open Directory (`⌘⇧O`), Settings (`⌘,`), each
  with a description and the key bound now. **Recent**: sessions, then directories, newest first; with none, the big `o▌`.
- Entries are a contribution point. An entry whose command isn't available is dimmed with a `soon` tag; New Session and
  Resume a Session stay that way until sessions exist (AI-4, SE-1).
- `j`/`k` or arrows move, `⏎` runs, `⌫` (or the `✕` on a recent under the pointer) forgets a recent, until it's opened again. The same actions are commands, so they're in the omnibox too.
- The pointer moving onto a row selects it, as `j`/`k` do.
- It fits the window: the columns shrink to fit, and below 760 wide Start and Recent become tabs. Taller than the window, it
  scrolls from the top.
- **Open Directory** opens a directory as it is, git repo or not: no worktree, no branch. It's a picker over typed paths
  and directories under `~`, with a **Browse…** row for the OS's folder chooser. The window reopens on it: every
  extension starts again there. `oriel <dir>` does the same. With sessions, this becomes the **direct** kind (`sessions.md`).
- Keys: the empty main area has its own keymap mode, `empty`, before `n` and `global`, so `␣␣` and the rest still work.

## 01f · The rail

The icon strip on the far left of 01: Explorer `⌘⇧E`, Search `⌘⇧F`, Git `⌘⇧G`, Requests `⌘⇧R`, Debug `⌘⇧D`, Tests `⌘⇧T`,
with Settings pinned at the bottom.

- A contribution point: icon, title, badge, and the command it runs. Plugins add entries the same way.
- An entry whose command isn't available yet is dimmed. Clicking the active entry hides its dock.
- An available entry lights under the pointer.

## 01g · Project overview: a directory or session with nothing open

![40-overview](mockups/40-overview.png)

What an open directory or session shows with no buffer open: right after opening it, and when its last tab closes.

- One card per repo: name, path, branch and ahead/behind; then panels for languages, commit activity over the last
  year, and contributors (or largest files). A plain directory gets languages, size and largest files only.
- Panels are contributions. The view shows at once; panels fill in from the background and are cached per repo by HEAD.

## 01c · Explorer scopes: Changed · Uncommitted · All

![22-explorer](mockups/22-explorer.png)

- A segmented control on top of the Explorer, with counts. `⌥1` `⌥2` `⌥3` switch scopes; the choice is remembered per session.
- **Changed**: every file the branch changes against its base (merge-base), uncommitted work included.
  - Opening a file here opens its **branch diff**, not the plain file. This is the review surface.
  - Each file has a dot: uncommitted, committed with more on top, or committed only.
  - Each file has a **read bar** (the review ledger per file). Sort by tree, flat, or "by review" (unread first).
- **Uncommitted**: working tree against HEAD, split into Staged, Unstaged and Untracked.
  - Stage or unstage whole files or single hunks with fugitive-style keys (`s`, `u`, `x`, `=`). Commit from here.
- **All**: the whole worktree, like any IDE. Changes are marked by columns you can toggle:
  - **status** letter (M A D R ? and U for conflicts), and the filename tinted to match.
  - **± lines**, with a tiny add/delete bar for the size of the change.
  - **agent**: an orange diamond where agent-written lines are unread, hollow once you've read them.
  - **threads**: the open-thread count, coloured by status. **diag**: error and warning counts.
  - Folders roll up (a dot plus the count of changed files beneath). Ignored files are dimmed; clean files show nothing.
  - `⌥C` toggles "changed only", which filters All down to Changed without leaving the tree.
- Multi-project workspaces show each project as a root with its branch and ahead/behind.
- The same markers appear on editor tabs and in the omnibox file results.

## 01d · Layout: any pane, anywhere

![24-layout](mockups/24-layout.png)

One layout model for every view. The terminal isn't special, and neither are
Requests, the agent console, Problems, Tests, Debug, a diff, or the Explorer.

- **Five placements** for any view:
  - **Docked**: bottom, left or right edges, each with its own tabs. `⌃J` toggles the bottom dock.
  - **Split in main**: a window in the vim window tree, next to your code.
  - **Tab in main**: a full main view among your file tabs.
  - **Float**: a movable overlay window.
  - **Drop-down**: a quake-style slide from the top, `` ⌃` ``.
- **Zoom** is separate from placement: `⌃W Z` makes the focused pane fill the window, and
  pressing it again restores the exact layout (tmux's `prefix z`). It works from any placement.
- **Moving a pane**: `⌃W M` opens the move menu, or use the direct keys: `⌃W J` dock bottom, `⌃W L` split right,
  `⌃W T` tab in main, `⌃W F` float. Dragging a tab onto any edge works too, and the drop targets glow.
- **Docks are the edges of the same window tree**, not a separate system. So `⌃W h/j/k/l` moves
  focus between code, a split terminal and the dock without thinking about which is which.
- **Terminals behave like nvim's**: insert mode types into the shell. `⌃\ ⌃N` switches to terminal-normal
  mode, where you scroll, search and yank the scrollback with vim motions. The mode shows in the statusline.
- **Opening one**: `:term` follows your default placement (bottom dock out of the box). `:term!` opens in main,
  and `:vterm`/`:sterm` split. A session's agent PTY (Claude) is just another terminal view.
- **Layouts are saved per session** and restored on switch. Named layouts (`:Layout save review`) can be
  bound to keys, so one key flips the whole screen between "write" and "review" arrangements.
- Plugins and Lua set placement too: `oriel.view.open("requests", { place = "main", split = "right" })`.

## 02 · ⌘K omnibox

![02-omnibox](mockups/02-omnibox.png)

- One ranked list: sessions, commands, files, symbols, tickets and PRs.
- Prefixes narrow the scope: `@` files, `#` symbols, `>` commands, `!` tickets.
- Preview pane: the highlighted session's waiting question, tasks and last moves, so you can answer without switching.
- Built from SH-4: `⌘K` opens it; `⇥` steps through the scopes; each source is a registry picker source, so its
  preview is the source's own. Symbols come from the open buffers' outlines until LSP's workspace symbols (LS-2);
  tickets and PRs from their plugins (PL-8, the GitHub integration). The session preview grows its question, tasks
  and moves as the AI tickets add them.

## 02b · Pickers: files, grep, keymaps, and the rest

![26-picker](mockups/26-picker.png)

The centred fuzzy-finder boxes you know from snacks.nvim (`Snacks.picker`), telescope and fzf-lua.

- **One picker UI, many sources.** An input with live fuzzy matching, a results list, and a preview of the selection.
  `⇥` cycles source without losing the query.
- **Default keys** (all rebindable):

  | Key | Source |
  |---|---|
  | `␣␣` | files (the quick jump) |
  | `␣/` | live grep (ripgrep on the worker, regex, streamed as you type) |
  | `␣*` | grep the word under the cursor |
  | `␣,` | open buffers |
  | `␣s` / `␣S` | symbols in this file / workspace (LSP) |
  | `␣k` | **keymaps**: every mapping with its mode, description and source; `⏎` runs it, `⌃e` jumps to where it's defined |
  | `␣:` | commands |
  | `␣t` | threads/requests |
  | `␣gl` / `␣gs` | git log / git status |
  | `␣h` | help (Oriel's docs and `:help`) |
  | `␣'` | resume the last picker exactly where you left it |

- **Ranking**: fuzzy score first, then changed on this branch, recently opened, already open, and shallower paths.
  fzf query syntax works: `'exact`, `!not`, `^prefix`, `suffix$`.
- **Actions on results**: open, vsplit (`⌃v`), split (`⌃s`), tab (`⌃t`), send to quickfix (`⌃q`), and
  **attach to a request** (`⌃a`, which gives a file or grep hit to the agent as context). `⇥` multi-selects.
- **Scope** follows the session: every project in the workspace, or narrowed with `@project`.
- **The omnibox (`⌘K`) is the same engine with every source merged.** Pickers are one source, centred, with preview.
- **The matcher is our own**, a Go implementation of fzf-style scoring (per `deps.md`), and runs on the app for
  file and buffer lists. Grep and workspace symbols stream from the worker.
- **Pickers are a plugin API**: `oriel.picker.source{ name, items = fn, preview = fn, actions = {…} }`.
  Tickets, PRs, sessions and themes are all sources. The themes picker (`␣T`, `:Themes`) shows each theme in the
  whole window as it's highlighted; `↵` keeps it in settings.toml, `esc` puts the old one back (a source's `Hover`
  and `Cancel`).
- Built in SH-5: `⇥` steps to the next source keeping the query, and `⌃␣` multi-selects (the mockup gives `⇥`
  both; the source strip keeps it). An item with a place (a path and line) gets open, `⌃v`, `⌃s`, `⌃t`, `⌃q` and
  `⌃a` from the picker; `⌃a` runs `request.attach`, which the requests (AI-5) provide. Sources today: files,
  grep (Go's regexp in the app, off the UI's goroutine, until the worker runs ripgrep), buffers (`:ls`), symbols
  here and in the open buffers, keymaps (`⌃e` to the definition), commands, sessions and quickfix (`:copen`).
  Ranking is the fuzzy score, then open buffers and shallower paths; "changed on this branch" and "recently
  opened" join with FG-1 and the session's history. Threads, git log and status, and help come with their tickets.

## 02c · Which-key: the next keys, as you type

![27-whichkey](mockups/27-whichkey.png)

- Press a prefix (leader, `g`, `z`, `]`, `⌃W`…) and pause: after **300 ms** a panel lists every key that can
  come next, with its **description**. Groups (`+agent`, `+git`) show in violet with their size.
- `⌫` backs up one key and `esc` cancels. `?` opens the keymaps picker filtered to this prefix.
- Descriptions come from each mapping's `desc` (`oriel.keymap.set("n", "<leader>al", fn, { desc = "Staging logs tool" })`).
  Your own maps show their source file.
- It's driven by the input engine's `Pending()` and `Keymaps()` (see `editor.md`), so it works for vim, plain,
  emacs or any engine, and inside panels (Requests, Explorer, the picker itself) with their own keymaps.
- It ships as a **built-in plugin** using only the public API. You can turn it off, restyle it, or replace it.
- Built in SH-6: `Host.Pending()` gives it the keys waiting for more (a mapping's, or the focused editor engine's)
  and the engine's own next keys, which the vim engine gives in Oriel's words for `g`, `z`, `[`, `]` and `⌃W`. A
  keymap with no command names a group (`{Keys = "<leader>a", desc = "+agent"}`). `⌫` and `esc` act on a
  mapping's keys, not an engine's half-typed command (which `esc` cancels in the engine). `?` to the keymaps
  picker comes with Lua's keymap API (PL-1). `whichkey.enabled` turns it off.

## 03 · Mission control (`⌘⇧K`)

![03-switcher](mockups/03-switcher.png)

- Every session as a card, grouped by worker. The border colour is the status.
- **Working** cards show the agent's live terminal. Every other status shows a
  structured "ask": permission with Allow/Deny inline, the CI failure, the
  question it's waiting on.
- Left: the **needs you** queue, oldest first, plus agentbus questions with their auto-answer countdowns.
- Bottom strip: usage per provider, worker CPU sparklines, agentbus presence.
- Fully keyboard-driven: `hjkl`, `⏎` opens, `a` allows, `x` stops.

## 04 · New session

![04-launch](mockups/04-launch.png)

- Prompt first. A pasted ticket ID expands into a card.
- Mode: Plan · Work · Review PR · Tour · Job · **New repo** (`sessions.md`). Provider: Claude or Codex, with model, effort and remaining usage.
- Repos on one shared branch, a worker, sandbox and network policy.
- Pre-flight: what will be fetched and built, and which LSPs will start.

## 05 · Plan mode

![05-plan](mockups/05-plan.png)

- Plan docs render as documents, with threads in the margin anchored to lines.
- The agent's live edits show as an inline diff block ("agent edited 4s ago").
- **Readiness**: Start work unlocks when every thread is resolved or deferred.

## 06 · Review, and the review ledger

![06-review](mockups/06-review.png)

- Split diff with intraline highlights. Each hunk has a "reviewed" toggle. Explain works per **code unit**, not per hunk (see 07b).
- **Review ledger** bar: agent-written lines read, skimmed and unread. Commit is gated until you've read them (configurable).
- Authorship breakdown: Claude, you, gofmt.
- **Cross-model AI review**: Codex reviews Claude's work (or the other way round), with findings sorted by severity.
- GitHub threads mirrored inline. The agent drafts a reply, and **you** forward it.

## 07 · Explain this change

![07-explain](mockups/07-explain.png)

- A step dock with ← → across files and repos. Code outside the step dims.
- Notes pinned to ranges with leader lines: the annotation house style.

## 07b · Explain by code unit, and "Explain this"

![25-explain-unit](mockups/25-explain-unit.png)

- **Units, not hunks.** A `?` sits on the smallest syntax node that owns a change: a function, a method,
  a `case`, an `if`, a loop, a closure, a type. One hunk can touch three units, and one unit can span two hunks.
- Units come from **tree-sitter** (blocks, conditions, loops: every language, offline).
  **LSP** adds symbol names and kinds (`documentSymbol`) and `selectionRange` where the server has it.
- **`[` / `]` widen and narrow** along the syntax tree. The breadcrumb in the card
  (`func roundHalfEven › switch diff › case 0`) shows where you are, and each crumb is clickable.
- **Explain this, anywhere**: select code in any file (not only diffs), terminal output, or a
  debugger frame, then right-click → **Explain this**, or `␣ e`. In normal mode `␣ e` explains the unit under the
  cursor, and pressing it again widens. The same menu has *Ask about this…* (opens a request
  anchored to the selection), *Review this*, *Write tests for this*, *Refactor this…*, and LSP code actions.
- **Depth**: *brief* by default, *detailed* on demand, and *why · history*, which pulls in git blame, the commit,
  the PR and any plan or threads that touched it.
- **Who answers**: a fork of the session's agent (`$.model.fork`), so it knows why the code is the
  way it is. Outside a session, the resident utility session answers. It never joins the main conversation.
- **Cached per unit**, keyed by the unit's content hash. An edit *inside* the unit marks it "changed since"
  and offers a refresh. Edits elsewhere don't invalidate it.
- *Pin as note* turns an explanation into a resolved thread on that range. *Ask a follow-up* turns it into a request.
- The **Changed units** panel lists every unit the branch touched, with its explain status. Explaining
  by unit doubles as a review checklist that follows the code's structure rather than the diff's.

## 08 · Debugging

![08-debug](mockups/08-debug.png)

- DAP (delve here): breakpoints, conditional breakpoints, inline values, call stack, variables, watch, REPL.
- **Ask about this frame**: the agent reads the paused frames through debugger tools, with scoped access shown as chips.

## 09 · LSP and vim

![09-lsp](mockups/09-lsp.png)

- Completion with docs, signature help, hover, inlay hints, diagnostics.
- Code actions include an **Agent** group ("ask Claude to fix in a thread").
- References peek inline, as an embedded editor.
- Vim `:s` with live match highlights, offering "use LSP rename" instead.

## 10 · HUD (hold `⌥`)

![10-hud](mockups/10-hud.png)

- The control room as an instrument cluster: usage with **burn-rate projection**,
  workers, sessions by status, today's stats, the log tail, agentbus, the CI queue.
- Release to hide, click to pin. Nothing here is a separate screen.

## 11 · Comms (the assistant)

![11-assistant](mockups/11-assistant.png)

- The room's assistant as a drawer on `/`: "what needs me?", "review this PR".
- Answers come back as actionable rows (number keys pick one). Every action has an undo with a countdown.
- It shows what it's allowed to do, and its data sources with how fresh each is.

## 12 · Inbox and interrupts

![12-inbox](mockups/12-inbox.png)

- Cards stack bottom-right: agentbus questions (countdown ring), review
  requests, network and gateway approvals, security-key touch.
- Tabs carry badge counts. The Inbox panel shows the full history.
- Approvals show the review state ("0 unreviewed lines ✓") right where you approve a push.

## 13 · Verify & raise

![13-verify](mockups/13-verify.png)

- The quiz before the PR, as a chat. Each answer gets a verdict, and a partial one points at the file to read.
- A skip is allowed, and logged.

## 14 · Tours

![14-tour](mockups/14-tour.png)

- Prose, quoted code (real files, real line numbers) and pinned notes. The note numbers recur in the diagram.
- Ask about any step. Export to a single HTML file.

## 15 · Agent console (Codex here)

![15-agent](mockups/15-agent.png)

- Structured transcript: tool calls as blocks, edits as mini-diffs, shell output folded.
- Subagents box, permission card (and the rule "Always" would create), network approval.
- Composer with `@file`, model and effort chips, queued messages.

## 16 · Plugins

![16-plugins](mockups/16-plugins.png)

- The mockup still says "Extensions", `extensions.toml` and COMMUNITY. The page is **Plugins**, the file is
  `settings.toml`, and badges are reviewed, trusted or unverified (`catalog.md`, PL-21).
- The REGISTRY tab browses the plugin registries. Installing writes a line to `[plugins]`.
- Every plugin shows what it contributes, the permissions it holds, how it runs (in-process, process or wasm) and live resource use.

## 17 · Light theme

![17-light](mockups/17-light.png)

- The same screen as 01, with only the theme tokens changed. That proves the tokens are complete.

## 18 · Ship (Git + PR)

![18-git](mockups/18-git.png)

- A flight checklist per project: uncommitted → behind base → unpushed → PR → checks → merge.
- Multiple projects side by side, with cross-linked PRs and a merge order.
- "Before this ships" lists the gates: plan, threads, ledger, tests, verify, AI review.

## 19 · Your config, live

![19-config](mockups/19-config.png)

## 20 · `:Inspect`

![20-inspect](mockups/20-inspect.png)

## 21 · Theme editor (`:Theme`)

![23-theme](mockups/23-theme.png)

- Pick mode: click anything and see its token chain (component ← semantic ← palette), then choose which link to edit.
- An OKLCH picker, derived or fixed values, and contrast and distinguishability lint on the spot.
- Every edit is a minimal override written to your theme TOML, shown live at the bottom. Details are in `theming.md`.

## 22 · Errors and the log (`:Log`)

![37-errors](mockups/37-errors.png)

- Error and warning toasts at the top right, with actions; the ✕/⚠ counts in the status line.
- A config error marks its line in the file, and the toast's **Edit init.lua** link goes there; saving reloads.
- The Log tab: Oriel, Session and All scopes, level chips, a filter, and an entry open on its traceback with the
  failing source highlighted. Details are in `errors.md`.

![38-crash](mockups/38-crash.png)

- After a crash, the next launch shows the report with **Copy report** and a link to a new GitHub issue. Nothing is sent.

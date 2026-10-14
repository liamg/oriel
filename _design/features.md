# Feature inventory

Every capability the mockups show or the docs promise, with where it lives, when it lands, and a **testable**
definition of done. This is the contract for "the app the screenshots imply". A feature isn't done until its
acceptance check passes in CI (or, where noted, in a scripted manual check recorded in the PR).

Phases are from `PLAN.md`: **P0** spike · **P1** framework · **P2** editor core · **P3** shell · **P4** IDE · **P5** AI ·
**P6** review · **P7** the rest · **P8** plugin registry. **After** lists the tickets that must be done first; the order in `_tickets/README.md` is generated from it.

## Framework (first, after the spike)

How everything hangs together, built and tested before features land on it.

| ID | Feature | Mockup | Module | Phase | After | Done when |
|---|---|---|---|---|---|---|
| FW-1 | Module skeleton and core interfaces wired end to end: app → `core.Store` → registry → a view | – | all | P1 | P0-1 | an empty app boots; a registered command runs from a keymap and an event updates a view; an import-rule test fails if `text`/`vim`/`core`/`agent` import `ui` or `render` |
| FW-2 | Event store: typed events, materialised `core.State`, subscriptions, replay | – | core | P1 | FW-1 | replaying a recorded event log reproduces the state byte-for-byte |
| FW-3 | Registries: commands, keymaps (modes, chords, panel-local), options (scoped) | 19 | registry, input | P1 | FW-1 | `:Keys`/`:Commands` list sources; chords resolve with timeouts; scope precedence tests |
| FW-4 | `ui` toolkit core: `Ctx`, ids, flex layout, anchors, scroll, clipping, z-order | – | ui | P1 | FW-1, SH-1 | layout golden tests; a 10k-row scroll at 120 Hz |
| FW-5 | Interaction states: hover, press, focus rings, disabled, cursors, tooltips; spring animation | all | ui | P1 | FW-4 | display-list snapshots per state; 200 hover targets stay within the frame budget; reduced-motion respected |
| FW-6 | Widgets: buttons, chips, inputs, virtual lists, trees, tabs, menus, context menus, modals, popovers, toasts | all | ui | P1 | FW-5, FW-7 | each widget has snapshot tests in dark and light, keyboard and mouse paths, and a help id |
| FW-11 | `toml` module: TOML 1.0 parser and round-tripping editor | – | toml | P1 | P0-1 | the `toml-test` suite passes; set, add and delete a key leave every other byte unchanged |
| FW-7 | Theme tokens: layered resolution, live reload, the default black/yellow/orange theme | 17, 23 | theme | P1 | FW-4, FW-11 | token resolution tables; the fixture renders in every built-in theme |
| FW-8 | Headless backend: display-list snapshots plus perceptual PNG diffs against the mockups | – | render | P1 | FW-4 | CI renders the fixture workspace and diffs it within threshold |
| FW-9 | Layout tree and tab groups: **drag any pane to any tab group, dock, split or float**, animated drop targets, zoom, `⌃W` keys | 24 | layout | P1 | FW-6, FW-8 | snapshot tests for every move and drop target; drag and drop scripted in tests; layouts saved and restored |
| FW-10 | Accessibility tree emitted from widget ids | – | ui | P1 | FW-6 | the fixture's tree has its main regions with roles and names (screen readers: FW-12) |
| FW-12 | Platform accessibility bridges: NSAccessibility (macOS) and AT-SPI (Linux) | – | render/gio | P1 | FW-10 | VoiceOver and Orca read the fixture's main regions and press a button (scripted manual check) |
| FW-13 | UI motion polish: docks slide, menus, popovers and modals fade in from their anchor, toasts slide, tab underlines and selections glide, hover eases; shared durations as theme effects; exits for keyed nodes | – | ui, widget, layout | P3 | FW-5, FW-6, FW-9 | start, midpoint and settled snapshots per animation; `ui.reduced_motion` jumps to settled; nothing over 250 ms or delaying input; frame budgets hold |

## Shell and navigation

| ID | Feature | Mockup | Module | Phase | After | Done when |
|---|---|---|---|---|---|---|
| P0-6 | Glyph-atlas prototype in the spike; re-measure on macOS; go/no-go | 01 | spike | P0 | P0-4 | budgets 1, 4 and 6 pass on macOS, or the report recommends the fallback |
| SH-1 | Window, GPU rendering, HiDPI and 1× text | 01 | render, typeset | P0 | P0-4, P0-6 | spike budgets met on macOS and Linux (`shell.md`); `01-ide` re-created natively and side by side with the PNG |
| SH-2 | Session tabs with status dots and `⌘1–9` | 01 | app | P3 | FW-6, FW-2 | 7 statuses render correctly from a fixture `core.State`; keys switch in under one frame |
| SH-4 | ⌘K omnibox (sessions, commands, files, symbols, tickets, PRs) with prefixes | 02 | app, picker | P3 | FW-6, FW-3 | fuzzy ranking golden tests; preview pane renders for each source |
| SH-5 | Pickers (`␣␣` files, `␣/` grep, `␣k` keymaps…) with preview and actions | 26 | picker | P3–P4 | SH-4 | each source returns, ranks and previews; `␣'` resumes; quickfix and attach actions work |
| SH-6 | Which-key popup | 27 | plugin (built-in) | P3 | FW-3, FW-6 | appears after 300 ms for any prefix, lists `desc`, `⌫` backs up |
| SH-7 | Mission control (`⌘⇧K`) with live terminals and inline asks | 03 | app | P7 | SH-2, AI-1 | grouped by worker; working cards stream output; Allow/Deny works from the card |
| SH-8 | HUD drawer (hold ⌥) and indicators in slots | 10, 01 | app, hud | P7 | FW-6, FW-2 | every panel bound to live events; indicators updatable from Lua |
| SH-9 | Inbox and interrupt cards (agentbus, network, gateway, security key) | 12 | app | P7 | FW-6 | each card type acts and dismisses; badge counts on tabs |
| SH-10 | Help everywhere (right-click → Help, `K`, `⌘?`, `:help`) | 28 | help | P3→ | FW-6 | CI lint: every interactive element resolves to a help page and anchor |
| SH-11 | Light, dark and theme editor (`:Theme`, pick mode) | 17, 23 | theme | P3, P7 | FW-7 | token resolution tests; the fixture renders in all built-in themes; pick-to-override writes the smallest TOML diff |
| SH-12 | Settings page (`:Settings`, ⌘,): sections, search, typed controls, scopes, modified and reset; `settings.toml` storage | 19 | app, registry | P3 | FW-3, FW-6, FW-11 | `settings.md` precedence tests; a GUI edit and a reset each change exactly one line of a hand-commented file; Lua-set options are read-only with their source |
| SH-13 | Errors, notifications and the log panel: one `Notified` event for every notice, an Oriel log and a log per session, the status line, toasts and ✕/⚠ counts, `:Log` with stack traces and highlighted source, `log/slog` to a ring and a rotating file, panics recovered per contribution, crash reports | 37, 38 | core, app, ui | P3 | FW-2, FW-6, FW-9, ED-6 | snapshots of each notice placement and the log panel; repeats collapse; panicking contributions are turned off and the app keeps going; crash report offered on next launch; secrets redacted; replay reproduces the history |
| SH-14 | Closing views (`⌃W c`, `q` on read-only views) and `:Keys`/`:Commands` as pickers | – | app | P3 | FW-9, SH-5 | every built-in view closes from the keyboard; `:Commands` filters and runs |
| SH-15 | Start view: what the window shows with nothing open | – | app | P3 | SH-14, ED-12, SH-4, SH-12 | shown at launch and when the last view closes; every entry runs its command; plugins add entries |
| SH-16 | The rail: main views down the left edge, as a contribution point | 01 | app | P3 | FW-9, SH-14 | snapshot against 01's strip; plugin entries appear, badge and toggle their view |
| SH-17 | Project sidebar: per-project panels, the session's entries, one tab strip across projects, adding a project | 42 | app, registry, core | P5 | SH-16, FG-1, FG-3, TR-3, AI-1, SE-1 | snapshots of 42's panels 1–5; ⌃n and headers switch projects and reopen their panels; Search per project with All projects; a Lua project entry under every project; tabs and breadcrumbs name the project; + adds a repo to the session |

## Editor

| ID | Feature | Mockup | Module | Phase | After | Done when |
|---|---|---|---|---|---|---|
| ED-1 | Rope, snapshots, anchors, authorship, undo tree | – | text | P2 | FW-1 | property tests against a naive model; fuzzing with no failures for 1 hour |
| ED-2 | Vim engine: motions, operators, text objects, registers, marks, dot, counts | 01 | vim | P2 | ED-1, P0-3 | the `.vt` suite passes, oracle mode passes, every mined vim test passes (or carries a reviewed `deviation:`) |
| ED-3 | Visual and visual-block, `:s`/`:g` with vim regex, ranges | 09 | vim | P2 | ED-2 | the same, plus the regex conformance suite |
| ED-4 | Macros, folds, splits, buffers, quickfix | – | vim | P2–P4 | ED-2, FW-9 | the same |
| ED-5 | `plain` engine and the `input.Engine` interface (emacs as a plugin) | – | input | P3 | ED-2, FW-3 | an engine swap at runtime per buffer; a sample emacs plugin passes its tests |
| ED-6 | Tree-sitter highlighting, folds, outline, code units | 01, 25 | syntax | P2 | ED-1 | highlight snapshots for Go/TS/Py/Rust/Lua/MD; edits re-parse incrementally in under 1 ms in a 600-line file, and no keystroke waits on a parse at any size |
| ED-7 | Decorations between lines: threads, explain cards, diffs, virtual text | 01 | ui, editor | P3 | ED-1, FW-6 | the layout engine places blocks at anchors and survives edits above them |
| ED-8 | Multi-cursor and LSP rename preview | 09 | edit | P4 | ED-7, LS-1 | – |
| ED-9 | `.editorconfig`, the IDE-relevant `.gitattributes` (eol, binary, encoding, generated), and opt-in modelines as a buffer-scoped settings layer | 19 | registry, edit | P3 | ED-2, SH-12 | EditorConfig core test suite passes; `.gitattributes` fixtures match `git check-attr`; properties show with their source on the settings page; edits re-apply live |
| ED-12 | Editor pane and files: the buffer drawn with highlights, cursor, selections and folds; keys through the keymap registry; the engine's windows as FW-9's panes; load and save (BOM, CRLF, atomic writes), modified, reload on change; `:w`/`:q`/`:e`; `oriel {file}` | 01 | editor, edit | P3 | ED-4, ED-6, FW-6, FW-9, SH-13 | pane snapshots and a golden PNG against the mockup; edits written byte-for-byte as nvim writes them; notices, `:ls`, quickfix and the clipboard go through the IDE; `:q` on changes and reload behave as vim; splits and drags change one tree; under 8 ms keypress to frame in a 10k-line file |
| ED-11 | Languages as plugins: a registry language contribution, the built-in languages as a built-in plugin, a compressed core set of ~25 grammars, an on-disk compile cache, local grammar packs | – | syntax, registry | P3 | ED-6, FW-3 | a registered language highlights and shadows a built-in; every core grammar has a golden fixture; binary grows < 12 MB (8 MB before P3 added languages); a cached grammar opens in < 20 ms; a dropped-in pack is used |
| ED-13 | Editor options in the registry: the engine's options as `OptionSpec`s with vim aliases, `:set` and the settings page on one value, `editor.clipboard`, and the option list checked against the registry | – | vim, registry | P3 | ED-12, SH-12 | the `.vt` suite passes with options from the registry; `:set` shows on the settings page; `editor.clipboard` switches `y`/`p` between the system clipboard and registers; the option-list test passes |
| ED-14 | Highlight queries without a parent walk per capture; re-highlight only what an edit changed | – | syntax | P3 | ED-6 | a query costs what its range holds wherever it is in the 10k-line fixture, each 1 KB chunk under 2 ms (16 KB is ~9 ms under wazero; see the ticket); worst keypress to frame under 8 ms; grammars rebuild byte for byte |
| ED-10 | PCRE2 regex outside the vim engine: project search, the `plain` engine, the plugin and Lua API | – | regex | P3 | ED-3 | PCRE2's own test suite passes for supported features; the Lua API takes PCRE2 patterns |

## Files, git, terminals

| ID | Feature | Mockup | Module | Phase | After | Done when |
|---|---|---|---|---|---|---|
| FG-1 | Explorer scopes: Changed, Uncommitted, All, with markers | 22 | gitx, app | P4 | FW-6, ED-6 | fixture repos produce the expected markers; `⌥1–3` switch scopes |
| FG-2 | Gutter hunks, stage and unstage hunks, inline blame by author kind | 06 | gitx | P4 | ED-7 | staging a hunk matches `git apply --cached` |
| FG-3 | Project search and replace preview | 26 | search | P4 | SH-5 | ripgrep on the worker streams results; replace shows a diff before applying |
| FG-4 | Terminals: Ghostty VT under wazero, our renderer, placements, terminal-normal mode | 24 | term | P4 | FW-9 | vttest-style suite; parse throughput ≥ 200 MB/s; snapshot on reconnect |
| FG-5 | Shell integration (OSC 133/7): prompt jumps, exit dots, send output to a request | 24 | term | P4 | FG-4 | – |
| FG-6 | Ship page: uncommitted → behind → unpushed → PR → checks, multi-project | 18 | gitx, gh | P6 | FG-2 | flows tested against fixture repos and a fake GitHub |
| FG-7 | Project overview: languages, size, commit activity, contributors and branch for each repo, shown when nothing's open in a directory or session | – | app, gitx | P3 | SH-15, ED-11 | shows in one frame; panels fill within budget on a 100k-file repo; closing the last tab returns to it |

## LSP, tests, debugging

| ID | Feature | Mockup | Module | Phase | After | Done when |
|---|---|---|---|---|---|---|
| LS-1 | Completion, hover, signature help, diagnostics, code actions, inlay hints | 09 | lsp | P4 | ED-7 | recorded gopls/tsserver/pyright transcripts replay to the expected UI state |
| LS-2 | Definition, references peek, rename, symbols, call hierarchy | 09 | lsp | P4 | LS-1 | the same |
| LS-3 | Agent LSP tools over MCP | – | lsp, agent | P5 | LS-1, AI-2 | agents can call definition/references/diagnostics read-only |
| TR-1 | ▶ gutter run for tests, subtests and table rows; ✓/✕ inline | 36 | testrun | P4 | ED-6, ED-7 | Go, pytest, vitest/jest and cargo adapters pass recorded-output suites |
| TR-2 | Inline failure block: want/got diff, output, jump, debug, **ask to explain**, ask to fix | 36 | testrun, app | P4 | TR-1 | a failing fixture test renders the block at the assertion line; explain returns a card without editing any file |
| TR-6 | "Ask to explain" next to every "ask to fix" (tests, diagnostics, CI, conflicts, failed terminal commands) | 36, 09, 18 | explain | P6 | TR-2, RV-5 | each fix entry point has an explain twin; explain runs tool-less |
| TR-3 | Tests panel: tree, filters, flaky detection, run history | 36 | testrun, app | P4 | TR-1, FW-6 | – |
| TR-4 | Watch mode (affected tests) and coverage gutter | 36 | testrun | P4 | TR-1 | a save re-runs only the affected packages in the Go fixture |
| TR-5 | Agent test runs as evidence | 21, 36 | testrun, agent | P5 | TR-1, AI-2 | an agent's "tests pass" claim links to a run id |
| DB-1 | DAP: breakpoints (conditional, logpoints), stepping, stack, variables, watch, REPL | 08 | dap | P7 | ED-7, FW-9 | dlv and debugpy recorded sessions plus live CI runs |
| DB-2 | Inline values while stopped; debug a single test case | 08, 36 | dap, testrun | P7 | DB-1, TR-1 | – |
| DB-3 | Agent debugger tools with scoped access | 08 | dap, agent | P7 | DB-1, AI-2 | the scope is enforced: eval allowed, step denied when not granted |

## Sessions and AI

| ID | Feature | Mockup | Module | Phase | After | Done when |
|---|---|---|---|---|---|---|
| AI-1 | Worker, wire, sessions, workspaces with multi-repo worktrees | – | worker, core | P5 | FW-2 | sessions live in workspaces of crit's layout, Oriel's own only |
| SE-1 | Session kinds plan, work, review, tour, job, new repo, direct and scratch (`sessions.md`) | 04 | core, worker | P5 | AI-1 | each kind starts from the launcher and the CLI; direct never creates a worktree; new repo inits and registers |
| SE-2 | Session lifecycle: worktrees from a fetched base, Start next PR, archive, resume after restart | 03, 18 | core, worker | P5 | SE-1 | archived sessions keep threads and transcripts searchable; restart restores the layout |
| SE-3 | Files outside the session open read-only; app opens on mission control | 01 | app | P5 | SE-1 | – |
| AI-2 | Claude provider: interactive PTY plus mod hooks (no `-p`) | 15 | agent/claude | P5 | AI-1 | conformance suite against a fake `claude` and a live smoke run |
| AI-3 | Codex provider: `app-server` (billing checked in P0) | 15 | agent/codex | P5 | AI-1 | the same |
| AI-4 | New-session launcher: modes, provider, repos, worker, sandbox | 04 | app | P5 | AI-1, FW-6 | each mode starts the right prompt pack |
| AI-5 | Requests: inline threads plus the main Requests view, composer, urgent, btw | 01, 21 | core, app | P5 | AI-2, ED-7 | a comment reaches the agent within 1 s; replies render as markdown with activity blocks |
| AI-6 | Agent console: structured transcript, subagents, composer | 15 | app | P5 | AI-2 | – |
| AI-7 | Permissions, three layers; network and gateway approvals | 15, 12 | agent, sandbox | P5 | AI-2, AI-11 | each layer holds or denies as `agents.md` describes |
| AI-8 | Plan mode: rendered docs, margin threads, readiness, Start work | 05 | app, plan | P5 | AI-5 | the crit plan flow works end to end |
| AI-9 | Tasks rail, session stats, usage windows | 01 | core | P5 | AI-2 | – |
| AI-10 | Commit and PR drafts when the agent goes idle | 18 | agent | P6 | AI-2, FG-6 | a turn ending idle with a draft missing or behind the code asks the agent once per change (fake provider); clean trees and branches with no work of their own aren't asked; the user's draft is never overwritten; the Ship page marks a stale draft. Built in AI-10: see agents.md "Drafts when the agent goes idle" and ui.md 18 |
| AI-11 | Sandbox, gateway MCP, egress proxy (ported) | – | sandbox | P5 | AI-1 | crit's tests ported and passing |
| AI-12 | Stats and Usage: session stats tab, global Stats grouped by repo, Usage page, their entry points; `widget.Chart` | 43 | worker, app, ui | P5 | AI-1, AI-2, AI-3, AI-9, SH-17 | a worker answers `stats.query` by repo with the split rule and by session, rescanning only what was appended; the pages match 43; each entry point opens its page |
| AI-13 | A lost agent login: detected in the provider (Claude's log and screen, Codex's errors), flagged on its worker's sessions, fixed from Oriel | 12 | agent, worker, app | P5 | AI-2, AI-3, AI-12 | a fake session whose log or screen shows the recorded signed-out error raises `AuthExpired`; its worker's sessions turn needs-you with the card; the notice, the indicator and the launcher show it; Log in opens the raw terminal with `/login`, and a login clears every flag |
| AI-14 | Stats and Usage follow-ups: a custom range with `widget.DatePicker`, tools counted per range, plan tier chips, the active span, a session's account at the time | 43 | worker, app, ui | P5 | AI-12 | a custom range picked with the date picker (mouse and keys) re-queries, and its tools match the fixture calls inside it; Usage shows the tier from a fixture account file, none when unknown; the Active tile reads "of Nh"; a session started before a login change stays with its first account |
| SE-4 | Jobs: saved prompts on the worker, runs as job sessions, the Jobs page, schedules | 44 | worker, app | P5 | SE-1, AI-12 | a job saved from the page or the launcher runs as a job session against a fake provider and its row shows status, repos, summary, cost by repo and duration; Open, Resume and Delete work; a blocked run shows in Requests; a schedule fires on a fake clock and skips while one runs; the page matches 44 |

## Review and explain

| ID | Feature | Mockup | Module | Phase | After | Done when |
|---|---|---|---|---|---|---|
| RV-1 | Split and unified diff, intraline, per-hunk reviewed | 06 | app, diff | P6 | ED-7, FG-2 | – |
| RV-2 | Review ledger and commit gate (hard, soft, off by session kind) | 06, 18 | core | P6 | RV-1 | the gate blocks; "commit anyway" writes the trailer. Built: `registry.LedgerOf`, `CommitGate`/`CheckCommit`, `ledger.gate`, `git.commit` and its dialog, `core.LinesSeen`/`CommitGateOverridden`, `oriel.ledger`; not yet: the gateway's commit gated, the PR gate |
| RV-3 | Cross-model AI review findings | 06 | agent | P6 | RV-1, AI-3 | with fake providers: a Claude session's change is reviewed by Codex (and back; the same provider on another model with one installed); findings sorted by severity in the diff's column, filtered, turned into threads, dismissed; a re-review reads only what changed since |
| RV-4 | GitHub review mirroring and Forward to GitHub | 06 | gh | P6 | RV-1, FG-6 | the agent never posts; the user's forward does |
| RV-5 | Explain by code unit, `[`/`]` widen and narrow, Explain this on a selection | 25 | explain, syntax | P6 | ED-6, AI-2 | units are correct for fixtures in 4 languages; cache invalidated only by edits inside the unit |
| RV-6 | Explain this change walkthrough | 07 | explain | P6 | RV-5 | a fake explainer's JSON steps over two projects walk with ←/→ and the step chips, the notes' lines lit and the rest dimmed; Ask about this step and Comment on L<n> make a request and a thread; an edit inside a step's lines (buffer or disk) discards it, one outside keeps it; the view matches 07 |
| RV-7 | Verify & raise quiz | 13 | agent | P6 | AI-5 | – |
| RV-8 | Tours: reader, 7 kinds, export | 14 | tour | P7 | RV-5 | crit's tour features ported |

## Platform

| ID | Feature | Mockup | Module | Phase | After | Done when |
|---|---|---|---|---|---|---|
| PL-1 | Lua config and plugins, `:Hooks`, `:Inspect`, LuaLS types | 19, 20 | script, registry | P3 | FW-3 | the `scripting.md` sample `init.lua` runs as written |
| PL-12 | Lua errors: source names and tracebacks, a notice plus a diagnostic on the line, the `:lua` REPL, last good config kept, hooks turned off after 3 errors | 19 | script | P3 | PL-1, SH-13 | fixture configs give the right `file:line`, frames and diagnostics; a broken reload keeps the old config; a failing hook is turned off and back on |
| PL-13 | Context menu entries from plugins: a menu-item contribution with where it applies; the built-ins on it; `oriel.menu.add` | – | app, registry, script | P3 | PL-1, SH-10 | a Lua entry shows only where its `when` says and runs; built-in entries come from the same point; Help stays last |
| PL-14 | Form widgets from Lua: input, toggle, select, and view state across redraws | – | script, app | P3 | PL-1 | a Lua panel's controls call its functions and redraw from its state; keyboard focus and help ids work; callback errors go through PL-12 |
| PL-15 | Drag and drop for plugins: drag sources, drop targets with kinds, OS file drops | – | ui, registry, script | P3 | FW-9, PL-1 | a Lua drop target gets only the kinds it takes; files dropped from the OS open or go to the target under the pointer; keyboard drag works |
| PL-2 | Process plugins with an OS sandbox; WASM plugins; `oriel build --with` | 16 | ext | P7 | PL-1 | the capability guard rejects undeclared calls; Landlock/sandbox-exec profiles applied |
| PL-3 | All-or-nothing install consent, update capability diff; grammar packs (CI-built, checksummed) | 29 | ext | P7 | PL-2, ED-11 | – |
| PL-4 | Comms assistant drawer (utility session, no `-p`) | 11 | assistant | P7 | AI-2 | – |
| PL-5 | agentbus: question cards with auto-answer, review requests, presence, log, auto-approve (as a helper process) | 12 | integ/agentbus | P7 | AI-1 | `crit-reference.md` → agentbus behaviour reproduced, against a fake bus |
| PL-8 | Linear: assigned tickets, start work from a ticket, state moves, PR links, omnibox source | 04, 02 | integ/linear (plugin) | P7 | PL-2, SE-1 | `crit-reference.md` → Linear, against a recorded Linear API |
| PL-9 | Slack PTAL via webhook | 18 | integ/slack (plugin) | P7 | PL-2 | – |
| PL-10 | Calendar: next meeting indicator (iCal URL or macOS EventKit) | 10 | integ/ical (plugin) | P7 | PL-2 | – |
| PL-11 | Prompts page (editable prompt packs, coding style written into CLAUDE.md), voice dictation | – | prompts, voice | P7 | AI-2 | `crit-reference.md` → Prompts and Voice |
| PL-6 | Self-update for the app and workers (self-signed, ed25519) | – | update | P7 | AI-1 | an update round-trips on macOS and Linux with rollback on a failed health check |
| PL-19 | Package format: Lua entry point, `oriel.process` / `oriel.wasm` / `oriel.asset`, `[contributes]` assets, `oriel plugin pack` | – | ext, script | P8 | PL-2, PL-3 | a data-only theme plugin, a Lua plugin and a Lua + process plugin each load from a directory; `pack` rejects an undeclared capability and an oversize plugin |
| PL-20 | `[plugins]` in `settings.toml`: install, update and uninstall from the file; `[plugin.<name>]` options; `oriel-lock.toml` consent | 16, 29 | catalog | P8 | PL-19, SH-12, SH-9 | adding a line installs and removing it uninstalls; a locked plugin activates silently on a fresh config dir; one asking for more stays inactive behind the consent card |
| PL-21 | Registry client: signed `index.json`, several registries, badges, trusted tags and hold, yank and revoke; the Plugins page and its Registry tab | 16, 29 | catalog, app | P8 | PL-20, PL-6 | against a fixture index: a bad signature is refused, a revoked version is disabled with a card, a trusted tag waits out `trusted_hold`; the page installs by writing `[plugins]` |
| PL-16 | The `liamg/oriel-plugins` repo and its CI: owner check, `pack`, reproducible Go archives, deps check, capability-diff comment, signed index release | – | tools (repo) | P8 | PL-19 | a fixture PR for a new version gets the comment; a merge publishes a verifiable `index.json` |
| PL-17 | `oriel plugin publish` opens the registry PR | – | cmd | P8 | PL-16, PL-21 | against a fake GitHub, publish forks, branches and opens a PR with the right `plugin.toml` change; on a trusted plugin it does nothing |
| PL-18 | Rename the `ext` package and `Extension` types to `plugin` | – | ext | P8 | PL-3 | no `Extension` identifier or "extension" UI string left outside file-extension code |

## What "Start work" does

Pressing **Start work** on this plan does **phase 0** only:

1. **Repo**: `liamg/oriel` (private) with `go.work`, the module skeleton from `architecture.md`, and `CLAUDE.md`/`AGENTS.md` carrying
   the rules from `testing.md`, `deps.md` and the coding style.
2. **CI**: macOS and Linux runners, the dependency allowlist, `go mod vendor` checks, `govulncheck`, `refcheck`, and the
   `vendor`ed `spec/` docs.
3. **Test harness**: `vimtest` with the `.vt` format and the nvim oracle, so P1 can start test-first.
4. **The Gio spike** (`cmd/oriel-spike`): the measurements in `shell.md` and a native re-creation of `01-ide`, ending in a written
   go/no-go report against the mockups.
5. **Codex billing check**: confirm `app-server` uses the subscription.

Each later phase then runs as its own session, planned against this inventory, with every row's "done when" written as tests
first. Mission control shows them side by side.

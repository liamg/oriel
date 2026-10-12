# Design

The design of Oriel, carried over from the planning session. **Start with `PLAN.md`**, then `features.md`
(the inventory every ticket comes from).

| Doc | Covers |
|---|---|
| `PLAN.md` | goal, decisions, phases, risks, everything decided |
| `features.md` | every feature: mockup, module, phase, "done when" |
| `sessions.md` | sessions not folders, kinds, lifecycle |
| `shell.md`, `rendering.md` | Gio, the render/ui toolkit, the layout tree, budgets, the spike |
| `editor.md` | text/rope, the vim engine, input engines, syntax, code units |
| `architecture.md` | processes, Go modules, interfaces |
| `agents.md` | providers (Claude via PTY and hooks, Codex), permissions, guardrails, commit gate |
| `plugins.md`, `scripting.md` | extension tiers, contribution points, hooks, Lua API, native plugins |
| `security.md` | capabilities, all-or-nothing consent, sandboxing plugins |
| `theming.md` | tokens, the theme editor, themes as plugins |
| `ide.md` | LSP, running tests, DAP, terminals (Ghostty), explorer scopes, git, pickers |
| `ui.md` | every screen, with mockups, the design language and the logo |
| `help.md` | help everywhere |
| `testing.md` | test-first, verified spec references, the nvim oracle |
| `deps.md` | the dependency policy and every approved or rejected module |
| `distribution.md` | signing, self-update, worker updates |
| `migration.md` | moving off crit |

Mockups: `mockups/*.png`. Sources are in `mockups/src` (HTML plus `base.css`); re-render with `mockups/render.sh`
(needs `chromium-headless-shell`). The logo sheets come from `mockups/*.py`.

These docs are the source of truth for behaviour. When a ticket and a doc disagree, raise it rather than guessing,
and update the doc in the same PR as the code.

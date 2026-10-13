# Tickets

Markdown tickets, one per file, generated from `_design/features.md`. Edit them freely; they're ours now.

- **Frontmatter**: `id`, `title`, `phase`, `status` (todo · doing · review · done), `area`, `module`, `depends_on`.
- **Order** comes from `depends_on`: a ticket starts when everything it depends on is `done`. Phases only group them.
  The suggested order below is a topological sort, phase-major.
- **Workflow**: start a session in this repo and name the ticket ("do PL-8"). The agent reads the ticket, then its
  design docs and mockups, checks its dependencies are done, writes the acceptance tests first, implements, and sets `status`.
- **Every ticket is built on the public APIs** a plugin would use; if one is missing, extending it generally is part of the ticket.
- New work gets a new ticket here (with `depends_on`), and a row in `_design/features.md` if it's user-facing.

## Ready now (no unmet dependencies)

- [ED-14](ED-14-highlight-queries-without-a-parent-walk-per-capture.md): Highlight queries without a parent walk per capture
- [FW-13](FW-13-ui-motion-polish-subtle-animations-for-panels-menus-and-popups.md): UI motion polish: subtle animations for panels, menus, popups and toasts
- [FW-12](FW-12-platform-accessibility-bridges-nsaccessibility-and-at-spi.md): Platform accessibility bridges: NSAccessibility (macOS) and AT-SPI (Linux)
- [SH-13](SH-13-errors-notifications-and-the-log-panel.md): Errors, notifications and the log panel: toasts, status line, :Log, slog, panics, crash reports
- [ED-5](ED-5-plain-engine-and-the-input-engine-interface-emac.md): plain engine and the input.Engine interface (emacs as a plugin)
- [ED-7](ED-7-decorations-between-lines-threads-explain-cards-.md): Decorations between lines: threads, explain cards, diffs, virtual text
- [PL-1](PL-1-lua-config-and-plugins-hooks-inspect-luals-types.md): Lua config and plugins, :Hooks, :Inspect, LuaLS types
- [SH-2](SH-2-session-tabs-with-status-dots-and-1-9.md): Session tabs with status dots and ⌘1–9
- [SH-4](SH-4-k-omnibox-sessions-commands-files-symbols-ticket.md): ⌘K omnibox (sessions, commands, files, symbols, tickets, PRs) with prefixes
- [SH-6](SH-6-which-key-popup.md): Which-key popup
- [SH-10](SH-10-help-everywhere-right-click-help-k-help.md): Help everywhere (right-click → Help, K, ⌘?, :help)
- [SH-11](SH-11-light-dark-and-theme-editor-theme-pick-mode.md): Light, dark and theme editor (:Theme, pick mode)
- [SH-12](SH-12-settings-page-settings-sections-search-typed-co.md): Settings page (:Settings, ⌘,) and settings.toml storage
- [ED-10](ED-10-pcre2-regex-outside-the-vim-engine.md): PCRE2 regex outside the vim engine
- [ED-11](ED-11-languages-as-plugins-core-grammar-set-and-local-packs.md): Languages as plugins: a language contribution, a core grammar set, local grammar packs
- [FG-1](FG-1-explorer-scopes-changed-uncommitted-all-with-mar.md): Explorer scopes: Changed, Uncommitted, All, with markers
- [FG-4](FG-4-terminals-ghostty-vt-under-wazero-our-renderer-p.md): Terminals: Ghostty VT under wazero, our renderer, placements, terminal-normal mode
- [AI-1](AI-1-worker-wire-sessions-workspaces-with-multi-repo-.md): Worker, wire, sessions, workspaces with multi-repo worktrees
- [SH-8](SH-8-hud-drawer-hold-and-tats-in-slots.md): HUD drawer (hold ⌥) and tats in slots
- [SH-9](SH-9-inbox-and-interrupt-cards-agentbus-network-gatew.md): Inbox and interrupt cards (agentbus, network, gateway, security key)

## Suggested order

Each ticket comes after everything it depends on; ties break by phase, then area.


### Phase 0 · spike and repo

1. [P0-1](P0-1-repo-skeleton-go-work-module-stubs-from-architec.md): Repo skeleton: go.work, module stubs from architecture.md
2. [P0-2](P0-2-ci-on-macos-and-linux-dependency-allowlist-vendo.md): CI on macOS and Linux: dependency allowlist, vendor check, govulncheck, refcheck, vendored spec/ · after P0-1
3. [P0-3](P0-3-vimtest-harness-the-vt-format-and-the-nvim-oracl.md): vimtest harness: the .vt format and the nvim oracle · after P0-1
4. [P0-4](P0-4-gio-spike-measure-the-budgets-re-create-01-ide-n.md): Gio spike: measure the budgets, re-create 01-ide natively, go/no-go report · after P0-1
5. [P0-5](P0-5-codex-app-server-billing-check-subscription-or-p.md): Codex app-server billing check: subscription or per-token
6. [P0-6](P0-6-glyph-atlas-prototype-in-the-spike-re-measure-on.md): Glyph-atlas prototype in the spike; re-measure on macOS; go/no-go · after P0-4
7. [SH-1](SH-1-window-gpu-rendering-hidpi-and-1-text.md): Window, GPU rendering, HiDPI and 1× text · after P0-4, P0-6

### Phase 1 · framework

7. [FW-1](FW-1-module-skeleton-and-core-interfaces-wired-end-to.md): Module skeleton and core interfaces wired end to end: app → core.Store → registry → a view · after P0-1
8. [FW-2](FW-2-event-store-typed-events-materialised-core-state.md): Event store: typed events, materialised core.State, subscriptions, replay · after FW-1
9. [FW-3](FW-3-registries-commands-keymaps-modes-chords-panel-l.md): Registries: commands, keymaps (modes, chords, panel-local), options (scoped) · after FW-1
10. [FW-4](FW-4-ui-toolkit-core-ctx-ids-flex-layout-anchors-scro.md): ui toolkit core: Ctx, ids, flex layout, anchors, scroll, clipping, z-order · after FW-1, SH-1
11. [FW-5](FW-5-interaction-states-hover-press-focus-rings-disab.md): Interaction states: hover, press, focus rings, disabled, cursors, tooltips; spring animation · after FW-4
12. [FW-11](FW-11-toml-module-toml-1-0-parser-and-round-tripping.md): toml module: TOML 1.0 parser and round-tripping editor · after P0-1
12. [FW-7](FW-7-theme-tokens-layered-resolution-live-reload-the-.md): Theme tokens: layered resolution, live reload, the default black/yellow/orange theme · after FW-4, FW-11
13. [FW-6](FW-6-widgets-buttons-chips-inputs-virtual-lists-trees.md): Widgets: buttons, chips, inputs, virtual lists, trees, tabs, menus, context menus, modals, popovers, toasts · after FW-5, FW-7
14. [FW-8](FW-8-headless-backend-display-list-snapshots-plus-per.md): Headless backend: display-list snapshots plus perceptual PNG diffs against the mockups · after FW-4
15. [FW-9](FW-9-layout-tree-and-tab-groups-drag-any-pane-to-any-.md): Layout tree and tab groups: drag any pane to any tab group, dock, split or float, animated drop targets, zoom, ⌃W keys · after FW-6, FW-8
16. [FW-10](FW-10-accessibility-tree-emitted-from-widget-ids.md): Accessibility tree emitted from widget ids · after FW-6
16. [FW-12](FW-12-platform-accessibility-bridges-nsaccessibility-and-at-spi.md): Platform accessibility bridges: NSAccessibility (macOS) and AT-SPI (Linux) · after FW-10

### Phase 2 · editor core

17. [ED-1](ED-1-rope-snapshots-anchors-authorship-undo-tree.md): Rope, snapshots, anchors, authorship, undo tree · after FW-1
18. [ED-2](ED-2-vim-engine-motions-operators-text-objects-regist.md): Vim engine: motions, operators, text objects, registers, marks, dot, counts · after ED-1, P0-3
19. [ED-3](ED-3-visual-and-visual-block-s-g-with-vim-regex-range.md): Visual and visual-block, :s/:g with vim regex, ranges · after ED-2
20. [ED-4](ED-4-macros-folds-splits-buffers-quickfix.md): Macros, folds, splits, buffers, quickfix · after ED-2, FW-9
21. [ED-6](ED-6-tree-sitter-highlighting-folds-outline-code-unit.md): Tree-sitter highlighting, folds, outline, code units · after ED-1

### Phase 3 · shell

20. [SH-13](SH-13-errors-notifications-and-the-log-panel.md): Errors, notifications and the log panel: toasts, status line, :Log, slog, panics, crash reports · after FW-2, FW-6, FW-9, ED-6
21. [ED-12](ED-12-editor-pane-files-and-the-first-usable-editor.md): Editor pane and files: open, edit, save, the first usable editor · after ED-4, ED-6, FW-6, FW-9, SH-13
22. [ED-5](ED-5-plain-engine-and-the-input-engine-interface-emac.md): plain engine and the input.Engine interface (emacs as a plugin) · after ED-2, FW-3
23. [ED-7](ED-7-decorations-between-lines-threads-explain-cards-.md): Decorations between lines: threads, explain cards, diffs, virtual text · after ED-1, FW-6
24. [PL-1](PL-1-lua-config-and-plugins-hooks-inspect-luals-types.md): Lua config and plugins, :Hooks, :Inspect, LuaLS types · after FW-3
24. [PL-12](PL-12-lua-errors-tracebacks-diagnostics-and-turning-off-failing-hooks.md): Lua errors: tracebacks, diagnostics in the file, turning off failing hooks · after PL-1, SH-13
25. [SH-2](SH-2-session-tabs-with-status-dots-and-1-9.md): Session tabs with status dots and ⌘1–9 · after FW-6, FW-2
26. [SH-4](SH-4-k-omnibox-sessions-commands-files-symbols-ticket.md): ⌘K omnibox (sessions, commands, files, symbols, tickets, PRs) with prefixes · after FW-6, FW-3
27. [SH-5](SH-5-pickers-files-grep-k-keymaps-with-preview-and-ac.md): Pickers (␣␣ files, ␣/ grep, ␣k keymaps…) with preview and actions · after SH-4
28. [SH-6](SH-6-which-key-popup.md): Which-key popup · after FW-3, FW-6
29. [SH-10](SH-10-help-everywhere-right-click-help-k-help.md): Help everywhere (right-click → Help, K, ⌘?, :help) · after FW-6
30. [SH-11](SH-11-light-dark-and-theme-editor-theme-pick-mode.md): Light, dark and theme editor (:Theme, pick mode) · after FW-7
30. [SH-12](SH-12-settings-page-settings-sections-search-typed-co.md): Settings page (:Settings, ⌘,) and settings.toml storage · after FW-3, FW-6, FW-11
30. [ED-9](ED-9-editorconfig-and-per-file-editor-conventions.md): .editorconfig and other per-file editor conventions · after ED-2, SH-12
30. [ED-10](ED-10-pcre2-regex-outside-the-vim-engine.md): PCRE2 regex outside the vim engine · after ED-3
30. [ED-11](ED-11-languages-as-plugins-core-grammar-set-and-local-packs.md): Languages as plugins: a language contribution, a core grammar set, local grammar packs · after ED-6, FW-3
30. [FW-13](FW-13-ui-motion-polish-subtle-animations-for-panels-menus-and-popups.md): UI motion polish: subtle animations for panels, menus, popups and toasts · after FW-5, FW-6, FW-9
30. [ED-14](ED-14-highlight-queries-without-a-parent-walk-per-capture.md): Highlight queries without a parent walk per capture · after ED-6
30. [ED-13](ED-13-editor-options-in-the-registry-and-editor-clipboard.md): Editor options in the registry: :set, the settings page, editor.clipboard · after ED-12, SH-12

### Phase 4 · IDE

31. [FG-1](FG-1-explorer-scopes-changed-uncommitted-all-with-mar.md): Explorer scopes: Changed, Uncommitted, All, with markers · after FW-6, ED-6
32. [FG-2](FG-2-gutter-hunks-stage-and-unstage-hunks-inline-blam.md): Gutter hunks, stage and unstage hunks, inline blame by author kind · after ED-7
33. [FG-3](FG-3-project-search-and-replace-preview.md): Project search and replace preview · after SH-5
34. [FG-4](FG-4-terminals-ghostty-vt-under-wazero-our-renderer-p.md): Terminals: Ghostty VT under wazero, our renderer, placements, terminal-normal mode · after FW-9
35. [FG-5](FG-5-shell-integration-osc-133-7-prompt-jumps-exit-do.md): Shell integration (OSC 133/7): prompt jumps, exit dots, send output to a request · after FG-4
36. [LS-1](LS-1-completion-hover-signature-help-diagnostics-code.md): Completion, hover, signature help, diagnostics, code actions, inlay hints · after ED-7
37. [ED-8](ED-8-multi-cursor-and-lsp-rename-preview.md): Multi-cursor and LSP rename preview · after ED-7, LS-1
38. [LS-2](LS-2-definition-references-peek-rename-symbols-call-h.md): Definition, references peek, rename, symbols, call hierarchy · after LS-1
39. [TR-1](TR-1-gutter-run-for-tests-subtests-and-table-rows-inl.md): ▶ gutter run for tests, subtests and table rows; ✓/✕ inline · after ED-6, ED-7
40. [TR-2](TR-2-inline-failure-block-want-got-diff-output-jump-d.md): Inline failure block: want/got diff, output, jump, debug, ask to explain, ask to fix · after TR-1
41. [TR-3](TR-3-tests-panel-tree-filters-flaky-detection-run-his.md): Tests panel: tree, filters, flaky detection, run history · after TR-1, FW-6
42. [TR-4](TR-4-watch-mode-affected-tests-and-coverage-gutter.md): Watch mode (affected tests) and coverage gutter · after TR-1

### Phase 5 · AI

43. [AI-1](AI-1-worker-wire-sessions-workspaces-with-multi-repo-.md): Worker, wire, sessions, workspaces with multi-repo worktrees · after FW-2
44. [AI-2](AI-2-claude-provider-interactive-pty-plus-mod-hooks-n.md): Claude provider: interactive PTY plus mod hooks (no -p) · after AI-1
45. [AI-3](AI-3-codex-provider-app-server-billing-checked-in-p0.md): Codex provider: app-server (billing checked in P0) · after AI-1
46. [AI-4](AI-4-new-session-launcher-modes-provider-repos-worker.md): New-session launcher: modes, provider, repos, worker, sandbox · after AI-1, FW-6
47. [AI-5](AI-5-requests-inline-threads-plus-the-main-requests-v.md): Requests: inline threads plus the main Requests view, composer, urgent, btw · after AI-2, ED-7
48. [AI-6](AI-6-agent-console-structured-transcript-subagents-co.md): Agent console: structured transcript, subagents, composer · after AI-2
49. [AI-8](AI-8-plan-mode-rendered-docs-margin-threads-readiness.md): Plan mode: rendered docs, margin threads, readiness, Start work · after AI-5
50. [AI-9](AI-9-tasks-rail-session-tally-usage-windows.md): Tasks rail, session tally, usage windows · after AI-2
51. [AI-11](AI-11-sandbox-gateway-mcp-egress-proxy-ported.md): Sandbox, gateway MCP, egress proxy (ported) · after AI-1
52. [AI-7](AI-7-permissions-three-layers-network-and-gateway-app.md): Permissions, three layers; network and gateway approvals · after AI-2, AI-11
53. [LS-3](LS-3-agent-lsp-tools-over-mcp.md): Agent LSP tools over MCP · after LS-1, AI-2
54. [SE-1](SE-1-session-kinds-plan-work-review-tour-job-new-repo.md): Session kinds plan, work, review, tour, job, new repo, direct and scratch (sessions.md) · after AI-1
55. [SE-2](SE-2-session-lifecycle-worktrees-from-a-fetched-base-.md): Session lifecycle: worktrees from a fetched base, Start next PR, archive, resume after restart · after SE-1
56. [SE-3](SE-3-files-outside-the-session-open-read-only-app-ope.md): Files outside the session open read-only; app opens on mission control · after SE-1
57. [TR-5](TR-5-agent-test-runs-as-evidence.md): Agent test runs as evidence · after TR-1, AI-2

### Phase 6 · review

58. [FG-6](FG-6-ship-page-uncommitted-behind-unpushed-pr-checks-.md): Ship page: uncommitted → behind → unpushed → PR → checks, multi-project · after FG-2
59. [AI-10](AI-10-commit-and-pr-drafts-when-the-agent-goes-idle.md): Commit and PR drafts when the agent goes idle · after AI-2, FG-6
60. [RV-1](RV-1-split-and-unified-diff-intraline-per-hunk-review.md): Split and unified diff, intraline, per-hunk reviewed · after ED-7, FG-2
61. [RV-2](RV-2-review-ledger-and-commit-gate-hard-soft-off-by-s.md): Review ledger and commit gate (hard, soft, off by session kind) · after RV-1
62. [RV-3](RV-3-cross-model-ai-review-findings.md): Cross-model AI review findings · after RV-1, AI-3
63. [RV-4](RV-4-github-review-mirroring-and-forward-to-github.md): GitHub review mirroring and Forward to GitHub · after RV-1, FG-6
64. [RV-5](RV-5-explain-by-code-unit-widen-and-narrow-explain-th.md): Explain by code unit, [/] widen and narrow, Explain this on a selection · after ED-6, AI-2
65. [RV-6](RV-6-explain-this-change-walkthrough.md): Explain this change walkthrough · after RV-5
66. [RV-7](RV-7-verify-raise-quiz.md): Verify & raise quiz · after AI-5
67. [TR-6](TR-6-ask-to-explain-next-to-every-ask-to-fix-tests-di.md): "Ask to explain" next to every "ask to fix" (tests, diagnostics, CI, conflicts, failed terminal commands) · after TR-2, RV-5

### Phase 7 · the rest

68. [DB-1](DB-1-dap-breakpoints-conditional-logpoints-stepping-s.md): DAP: breakpoints (conditional, logpoints), stepping, stack, variables, watch, REPL · after ED-7, FW-9
69. [DB-2](DB-2-inline-values-while-stopped-debug-a-single-test-.md): Inline values while stopped; debug a single test case · after DB-1, TR-1
70. [DB-3](DB-3-agent-debugger-tools-with-scoped-access.md): Agent debugger tools with scoped access · after DB-1, AI-2
71. [PL-2](PL-2-process-plugins-with-an-os-sandbox-wasm-plugins-.md): Process plugins with an OS sandbox; WASM plugins; oriel build --with · after PL-1
72. [PL-3](PL-3-all-or-nothing-install-consent-update-capability.md): All-or-nothing install consent, update capability diff · after PL-2
73. [PL-4](PL-4-comms-assistant-drawer-utility-session-no-p.md): Comms assistant drawer (utility session, no -p) · after AI-2
74. [PL-5](PL-5-agentbus-question-cards-with-auto-answer-review-.md): agentbus: question cards with auto-answer, review requests, presence, log, auto-approve (as a helper process) · after AI-1
75. [PL-6](PL-6-self-update-for-the-app-and-workers-self-signed-.md): Self-update for the app and workers (self-signed, ed25519) · after AI-1
76. [PL-8](PL-8-linear-assigned-tickets-start-work-from-a-ticket.md): Linear: assigned tickets, start work from a ticket, state moves, PR links, omnibox source · after PL-2, SE-1
77. [PL-9](PL-9-slack-ptal-via-webhook.md): Slack PTAL via webhook · after PL-2
78. [PL-10](PL-10-calendar-next-meeting-tat-ical-url-or-macos-even.md): Calendar: next meeting tat (iCal URL or macOS EventKit) · after PL-2
79. [PL-11](PL-11-prompts-page-editable-prompt-packs-coding-style-.md): Prompts page (editable prompt packs, coding style written into CLAUDE.md), voice dictation · after AI-2
80. [RV-8](RV-8-tours-reader-7-kinds-export.md): Tours: reader, 7 kinds, export · after RV-5
81. [SH-7](SH-7-mission-control-k-with-live-terminals-and-inline.md): Mission control (⌘⇧K) with live terminals and inline asks · after SH-2, AI-1
82. [SH-8](SH-8-hud-drawer-hold-and-tats-in-slots.md): HUD drawer (hold ⌥) and tats in slots · after FW-6, FW-2
83. [SH-9](SH-9-inbox-and-interrupt-cards-agentbus-network-gatew.md): Inbox and interrupt cards (agentbus, network, gateway, security key) · after FW-6
84. [PL-13](PL-13-context-menu-contributions.md): Context menu entries from plugins: a contribution point for right-click menus · after PL-1, SH-10 (added in P3, phase 4)
85. [PL-14](PL-14-lua-form-widgets.md): Form widgets from Lua: inputs, toggles, selects and a view's own state · after PL-1 (added in P3, phase 4)
86. [PL-15](PL-15-plugin-drag-and-drop.md): Drag and drop for plugins: drag sources, drop targets, and files dropped on the window · after FW-9, PL-1 (added in P3, phase 4)

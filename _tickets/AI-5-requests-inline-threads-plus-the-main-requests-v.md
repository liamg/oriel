---
id: AI-5
title: "Requests: inline threads plus the main Requests view, composer, urgent, btw"
phase: P5
status: todo
area: Sessions and AI
module: core, app
depends_on: [AI-2, ED-7]
---

# AI-5 · Requests: inline threads plus the main Requests view, composer, urgent, btw

**Phase** 5 · AI · **After** [AI-2](AI-2-claude-provider-interactive-pty-plus-mod-hooks-n.md), [ED-7](ED-7-decorations-between-lines-threads-explain-cards-.md) · **Unblocks** [AI-8](AI-8-plan-mode-rendered-docs-margin-threads-readiness.md), [RV-7](RV-7-verify-raise-quiz.md)

## Design

- `_design/agents.md`
- `_design/sessions.md`
- `_design/architecture.md`
- `_design/ui.md`
- `_design/features.md` (this row)

## Mockups

![01-ide](../_design/mockups/01-ide.png)
![21-requests](../_design/mockups/21-requests.png)

## Done when

a comment reaches the agent within 1 s; replies render as markdown with activity blocks

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.

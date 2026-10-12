---
id: AI-1
title: "Worker, wire, sessions, workspaces with multi-repo worktrees"
phase: P5
status: todo
area: Sessions and AI
module: worker, core
depends_on: [FW-2]
---

# AI-1 · Worker, wire, sessions, workspaces with multi-repo worktrees

**Phase** 5 · AI · **After** [FW-2](FW-2-event-store-typed-events-materialised-core-state.md) · **Unblocks** [AI-11](AI-11-sandbox-gateway-mcp-egress-proxy-ported.md), [AI-2](AI-2-claude-provider-interactive-pty-plus-mod-hooks-n.md), [AI-3](AI-3-codex-provider-app-server-billing-checked-in-p0.md), [AI-4](AI-4-new-session-launcher-modes-provider-repos-worker.md), [PL-5](PL-5-agentbus-question-cards-with-auto-answer-review-.md), [PL-6](PL-6-self-update-for-the-app-and-workers-self-signed-.md), [SE-1](SE-1-session-kinds-plan-work-review-tour-job-new-repo.md), [SH-7](SH-7-mission-control-k-with-live-terminals-and-inline.md)

## Design

- `_design/agents.md`
- `_design/sessions.md`
- `_design/architecture.md`
- `_design/ui.md`
- `_design/features.md` (this row)

## Done when

crit's workspace layout is read in place

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.

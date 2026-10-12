---
id: AI-11
title: "Sandbox, gateway MCP, egress proxy (ported)"
phase: P5
status: todo
area: Sessions and AI
module: sandbox
depends_on: [AI-1]
---

# AI-11 · Sandbox, gateway MCP, egress proxy (ported)

**Phase** 5 · AI · **After** [AI-1](AI-1-worker-wire-sessions-workspaces-with-multi-repo-.md) · **Unblocks** [AI-7](AI-7-permissions-three-layers-network-and-gateway-app.md)

## Design

- `_design/crit-reference.md`
- `_design/architecture.md`
- `_design/agents.md`
- `_design/features.md` (this row)

## Done when

crit's tests ported and passing

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.

---
id: TR-5
title: "Agent test runs as evidence"
phase: P5
status: todo
area: LSP, tests, debugging
module: testrun, agent
depends_on: [TR-1, AI-2]
---

# TR-5 · Agent test runs as evidence

**Phase** 5 · AI · **After** [TR-1](TR-1-gutter-run-for-tests-subtests-and-table-rows-inl.md), [AI-2](AI-2-claude-provider-interactive-pty-plus-mod-hooks-n.md) · **Unblocks** nothing yet

## Design

- `_design/ide.md`
- `_design/testing.md`
- `_design/features.md` (this row)

## Mockups

![21-requests](../_design/mockups/21-requests.png)
![36-tests](../_design/mockups/36-tests.png)

## Done when

an agent's "tests pass" claim links to a run id

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.

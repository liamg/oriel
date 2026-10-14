---
id: TR-1
title: "▶ gutter run for tests, subtests and table rows; ✓/✕ inline"
phase: P4
status: done
area: LSP, tests, debugging
module: testrun
depends_on: [ED-6, ED-7]
---

# TR-1 · ▶ gutter run for tests, subtests and table rows; ✓/✕ inline

**Phase** 4 · IDE · **After** [ED-6](ED-6-tree-sitter-highlighting-folds-outline-code-unit.md), [ED-7](ED-7-decorations-between-lines-threads-explain-cards-.md) · **Unblocks** [DB-2](DB-2-inline-values-while-stopped-debug-a-single-test-.md), [TR-2](TR-2-inline-failure-block-want-got-diff-output-jump-d.md), [TR-3](TR-3-tests-panel-tree-filters-flaky-detection-run-his.md), [TR-4](TR-4-watch-mode-affected-tests-and-coverage-gutter.md), [TR-5](TR-5-agent-test-runs-as-evidence.md)

## Design

- `_design/ide.md`
- `_design/testing.md`
- `_design/features.md` (this row)

## Mockups

![36-tests](../_design/mockups/36-tests.png)

## Done when

Go, pytest, vitest/jest and cargo adapters pass recorded-output suites

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.

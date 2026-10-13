---
id: ED-7
title: "Decorations between lines: threads, explain cards, diffs, virtual text"
phase: P3
status: done
area: Editor
module: ui, editor
depends_on: [ED-1, FW-6]
---

# ED-7 · Decorations between lines: threads, explain cards, diffs, virtual text

**Phase** 3 · shell · **After** [ED-1](ED-1-rope-snapshots-anchors-authorship-undo-tree.md), [FW-6](FW-6-widgets-buttons-chips-inputs-virtual-lists-trees.md) · **Unblocks** [AI-5](AI-5-requests-inline-threads-plus-the-main-requests-v.md), [DB-1](DB-1-dap-breakpoints-conditional-logpoints-stepping-s.md), [ED-8](ED-8-multi-cursor-and-lsp-rename-preview.md), [FG-2](FG-2-gutter-hunks-stage-and-unstage-hunks-inline-blam.md), [LS-1](LS-1-completion-hover-signature-help-diagnostics-code.md), [RV-1](RV-1-split-and-unified-diff-intraline-per-hunk-review.md), [TR-1](TR-1-gutter-run-for-tests-subtests-and-table-rows-inl.md)

## Design

- `_design/editor.md`
- `_design/testing.md`
- `_design/features.md` (this row)

## Mockups

![01-ide](../_design/mockups/01-ide.png)

## Done when

the layout engine places blocks at anchors and survives edits above them

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.

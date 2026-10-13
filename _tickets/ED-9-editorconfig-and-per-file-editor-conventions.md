---
id: ED-9
title: ".editorconfig and other per-file editor conventions"
phase: P3
status: todo
area: Editor
module: registry, edit
depends_on: [ED-2, SH-12]
---

# ED-9 · .editorconfig and other per-file editor conventions

**Phase** 3 · shell · **After** [ED-2](ED-2-vim-engine-motions-operators-text-objects-regist.md), [SH-12](SH-12-settings-page-settings-sections-search-typed-co.md) · **Unblocks** nothing yet

## Design

- `_design/settings.md` (precedence: a new layer under the buffer)
- `_design/editor.md` (options live at the layer they affect)
- `_design/security.md` (no modeline-style surprises)
- `_design/testing.md`
- `_design/features.md` (this row)

## Scope

- **EditorConfig**: `.editorconfig` files from the buffer's directory up to `root = true`, with the spec's glob
  matching and section order. Core properties map onto Oriel options: `indent_style`, `indent_size`, `tab_width`,
  `end_of_line`, `charset`, `trim_trailing_whitespace`, `insert_final_newline`, `max_line_length`.
  Unknown properties are ignored; a parse error shows with its line and keeps the file's other sections.
- Values apply as a **buffer-scoped `registry.Layer`**, shown with their source on the settings page
  ("from .editorconfig:12"). `:set` and window values still win.
- Our own parser and glob matcher (stdlib only); the EditorConfig spec and its core test suite are vendored under `spec/`.
- **Vim modelines**: off by default. When enabled, only an allowlist of display and indent options, never anything that runs code.
- **`.gitattributes`**: parsed with git's pattern rules (git-attributes(5), vendored), and only the attributes that
  change what the IDE does are used; the rest is git's business:
  - `eol`, `text` / `-text`: line endings on save, below `.editorconfig`'s `end_of_line`;
  - `binary` / `-text`: open read-only as binary, no line-ending conversion;
  - `working-tree-encoding`: the buffer's charset, below `.editorconfig`'s `charset`;
  - `linguist-generated`, `linguist-vendored`: marked as generated in the explorer, collapsed in diffs and the
    review ledger, and left out of project search unless asked.
- To decide before starting: other files, e.g. `.vscode/settings.json` indent keys, language formatter configs.

## Done when

the vendored EditorConfig core test suite passes; `.gitattributes` fixtures match `git check-attr`; a buffer opened under a fixture tree gets each property from the
right section, visible with its source on the settings page; editing `.editorconfig` re-applies live

## From SH-12

- Settings files are layers applied with `Options.SetFrom(name, layer, at, v, source)`; `.editorconfig` can do the
  same at `LayerBuffer`, with its path as the source, and the page shows it.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.

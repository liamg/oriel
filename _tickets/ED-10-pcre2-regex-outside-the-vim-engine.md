---
id: ED-10
title: "PCRE2 regex outside the vim engine"
phase: P3
status: review
area: Editor
module: regex
depends_on: [ED-3]
---

# ED-10 · PCRE2 regex outside the vim engine

**Phase** 3 · shell · **After** [ED-3](ED-3-visual-and-visual-block-s-g-with-vim-regex-range.md) · **Unblocks** [FG-3](FG-3-project-search-and-replace-preview.md)

## Design

- `_design/editor.md` (Regex)
- `_design/testing.md`
- `_design/features.md` (this row)

## Scope

- PCRE2's pattern syntax and matching (Liam, 2026-10-12) for everything that isn't the vim engine: project search
  and replace (FG-3), the `plain` engine's find and replace, and the plugin and Lua API.
- Our own Go implementation on the backtracking core `vimregex` already has (ED-3): a second parser, the same
  matcher. No cgo, no new dependency.
- The vim engine keeps vim's dialect (`/`, `?`, `:s`, `:g`): that's what vim users type and what the oracle checks.

## Done when

the cases from PCRE2's own test suite (`testdata/testinput*`/`testoutput*`, vendored under `spec/pcre2/`) that use
supported features pass, with the unsupported ones listed; the Lua API takes a PCRE2 pattern

## As built

- See editor.md, "As built (ED-10)". `spec/pcre2` holds PCRE2 10.44's testoutput1 and testoutput2 (BSD); 1826
  patterns pass, and the unsupported ones are listed in `vim/vimregex/testdata/pcre2-unsupported.txt`.
- The Lua half of "done when" waits for Lua (PL-1): `regex.Compile` is what `L.regex` binds.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.

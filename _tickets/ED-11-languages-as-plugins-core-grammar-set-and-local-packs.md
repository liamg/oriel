---
id: ED-11
title: "Languages as plugins: a language contribution, a core grammar set, local grammar packs"
phase: P3
status: todo
area: Editor
module: syntax
depends_on: [ED-6, FW-3]
---

# ED-11 · Languages as plugins: a language contribution, a core grammar set, local grammar packs

**Phase** 3 · **After** [ED-6](ED-6-tree-sitter-highlighting-folds-outline-code-unit.md), [FW-3](FW-3-registries-commands-keymaps-modes-chords-panel-l.md) · **Unblocks** grammar packs in [PL-3](PL-3-all-or-nothing-install-consent-update-capability.md)

## Design

- `_design/editor.md` (Syntax)
- `_design/plugins.md` (contribution points; registry interface)
- `_design/deps.md` (grammars built in CI from pinned sources)
- `_design/features.md` (this row)

## What

- **A language contribution** in the registry, `Registry.Language(l LanguageSpec) Disposable`: name, filetype
  patterns (extensions, file names, shebangs), the grammar `.wasm`, its queries (highlights, folds, injections, units)
  and the queries it inherits. Same stacking as every contribution: a later registration of a name shadows the
  earlier one, and disposing it restores it. Lua bindings follow with PL-1.
- **`syntax` looks languages up in the registry**, not in its own table. `ForPath` and `Lookup` go.
- **The built-in languages are a built-in plugin** (`builtin:languages`) that registers through that contribution,
  as a third-party pack would.
- **A core set of about 20–25 grammars, embedded compressed**: Go, TypeScript, TSX, JavaScript, Python, Rust, Lua,
  Markdown and inline Markdown, C, C++, Java, Ruby, Bash, JSON, YAML, TOML, HTML, CSS, SQL, Dockerfile, Make, Diff,
  Git commit, vimdoc. Each is added to `manifest.tsv` and `deps.md` as the current ones are, built by CI.
  A grammar is decompressed on first use.
- **wazero's compilation cache on disk** (in the user's cache directory), so a grammar compiles once per version.
- **Local grammar packs**: `~/.config/oriel/languages/<name>/` holding `grammar.wasm`, `queries/` and a manifest
  is registered at startup through the same contribution, and can shadow a built-in language. Grammars run sandboxed
  with no host imports, so loading one from disk needs no consent prompt.
- Downloading packs, signatures and consent are not here: they are PL-3's.

## Done when

- A test registers a language through the registry and gets highlights for a fixture; disposing it restores the
  built-in one it shadowed.
- Every core grammar has a highlight golden fixture that parses cleanly; CI rebuilds every `.wasm` and checks its
  checksum.
- The binary grows by under 8 MB for the core set.
- Opening a file in an already-compiled language is under 20 ms from cache, measured in a test.
- A grammar pack dropped into the languages directory is used for its files, and wins over a built-in of the same name.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.

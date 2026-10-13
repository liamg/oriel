---
id: shell/pickers
covers: [app.overlay.picker]
related: [shell/omnibox]
---

# Pickers

A picker searches one source: files, grep, buffers, symbols, keymaps, commands or the quickfix list, with a
preview of what's highlighted.

- `⇥` moves to the next source and keeps what you typed.
- `↵` opens; `⌃v` and `⌃s` open in a split, `⌃t` in a tab.
- `⌃q` sends the results (or the ones picked with `⌃␣`) to the quickfix list; `⌃a` attaches them to a request.
- `␣'` reopens the last picker where you left it.
- fzf's query syntax works: `'exact`, `!not`, `^prefix`, `suffix$`.

{{keys "picker.*"}}

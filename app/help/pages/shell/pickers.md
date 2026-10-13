---
id: shell/pickers
covers: [app.overlay.picker]
related: [shell/omnibox]
---

# Pickers

A picker searches one source: files, grep, buffers, symbols, keymaps, commands, the quickfix list or themes, with
a preview of what's highlighted.

- `⇥` moves to the next source and keeps what you typed.
- `↵` opens; `⌃v` and `⌃s` open in a split, `⌃t` in a tab.
- `⌃q` sends the results (or the ones picked with `⌃␣`) to the quickfix list; `⌃a` attaches them to a request.
- `␣'` reopens the last picker where you left it.
- `␣T` (or `:Themes`) picks a theme: the window shows each one as it's highlighted, `↵` keeps it in your
  settings.toml, and `esc` goes back to the one you had.
- fzf's query syntax works: `'exact`, `!not`, `^prefix`, `suffix$`.

{{keys "picker.*"}}

---
id: shell/overview
covers: [app.overview]
related: [shell/start, shell/pickers]
---

# The project overview

What an open directory shows while no file is open: right after you open it, and when you close its last tab. Each
repo gets a card: its branch and how far it is from its upstream, the languages in it by lines, a year of commits,
and who made them. A directory that isn't a git repo gets its languages, size and largest files instead.

- The card shows at once; each panel fills in as it's counted, in the background. They count again when HEAD moves.
- A `~` before the line count means some files were estimated from their size, to stay quick on a big repo.
- Plugins add their own panels with `oriel.overview.add`.

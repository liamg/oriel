---
id: files/search
covers: [search, search.replace]
related: [files/explorer, shell/pickers]
---

# Search

Searches every file in the project that git doesn't ignore, with ripgrep when it's installed. The query is a PCRE2
pattern (`(?<=return )Round\(`); **Aa** turns matching case off and on; **files** narrows it to globs (`*.go, *.ts`).
Generated and vendored files (by `.gitattributes`) are left out unless the query starts `@all `.

Matches stream in under their files as they're found. `⏎` or a click opens one at its line; **→ quickfix** sends them
all to the quickfix list.

## Replace {#replace}

Type a replacement (`$1`, `${name}` and `$$` as PCRE2 reads them) and each match shows what it becomes. **Replace
all…** opens the diff of every file it would change and writes nothing; **Apply** makes it. A file open in the editor
changes in its buffer, as one undoable step, and isn't saved; others are written. A file changed since the preview is
left alone.

{{keys "search.*"}}

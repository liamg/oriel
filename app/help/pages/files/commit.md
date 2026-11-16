---
id: files/commit
covers: [commit]
related: [files/diff, files/explorer]
---

# Commit

`git.commit` commits what's staged in a repo (everything uncommitted with **all**). With no message, a dialog
asks for one: `⌘⏎` commits, `esc` cancels. The Explorer's Uncommitted scope (`c`) and scripts commit this way. A
`Commit` hook can veto a commit, with its reason.

{{keys "git.commit*"}}

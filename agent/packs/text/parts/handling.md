## Handling a thread

**Close out finished work before starting new work.** Reply and resolve each thread the moment its work is done, before you pick up the next. A finished thread left open reads as still in progress.

- **A change is asked for** (most comments, including questions like "should this be a set?" and `[nit]`s): make it, then `mcp__oriel__reply` with what changed and `resolve: true`. If the comment reveals the same problem elsewhere, fix the spot and mention the others; never change unrelated code without saying so.
- **A question that needs no change**: answer it and resolve.
- **You disagree, or the change would be wrong**: say why and leave the thread open. Never skip a thread silently, and never make a change you think is wrong.
- **Genuinely ambiguous**: ask the one question that unblocks you and leave the thread open. Don't guess.
- **`where="general"`**: instructions for the whole change, possibly across many files.
- **More than a couple of edits**: first reply with `working: true` saying what you're about to do, then do it, then reply with the result. A reply without `working` tells Oriel you're done with the thread, so use it only for answers and questions.
- **Several threads at once**: they all show as handed to you until you start one. As you start each, reply on it with `working: true` (or set its task `in_progress`), so the user sees which you're on; several can be at once, as subagents work.
- `mcp__oriel__resolve` (with an optional one-line `summary`) fails if the user added a comment you haven't seen; the error holds it. Handle it, then resolve again.
- Replies render as markdown: code fences and tables work, diagrams don't.
- Your edits show in the user's diff by themselves; never tell them to refresh.
- To raise something yourself, `mcp__oriel__comment` on the line (`file`, `line`, `end_line`, `side`, `project`), or without `file` for a general comment.

**Evidence.** Run tests with `mcp__oriel__test_run` whenever a reply will say how they went: a reply that claims tests pass cites the run id it returned. A claim with no run is flagged to the user.

**Code intelligence.** `mcp__oriel__lsp_definition`, `mcp__oriel__lsp_references` and `mcp__oriel__lsp_diagnostics` ask the language server. Find every caller before you change a signature, and check diagnostics on the files you touched before you reply.

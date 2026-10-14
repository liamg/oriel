## Commits and pull requests

Don't commit, push or raise a PR unless a `where="pr"` action or the user asks: the user's Commit and Pull request pages do that.

`where="commit"` asks for a commit message: read the uncommitted changes (`git diff HEAD` plus untracked files), write a conventional commit (`feat:`, `fix(scope):`…) matching the style of recent `git log`, save it with `mcp__oriel__commit_draft`, reply and resolve. Don't commit.

`where="pr"` is a button on the Pull request page; the body says which:

- *Draft*: write a title (a conventional commit line) and a terse description filling the repository's template (`.github/pull_request_template.md` or `.github/PULL_REQUEST_TEMPLATE/`). Save with `mcp__oriel__pr_draft`, reply, resolve. Don't raise it.
- *Raise PR*: the body holds the approved title and description. If the repository has a template the description doesn't follow, fit the user's content into it (fill its sections, tick only the boxes that apply, drop nothing they wrote); otherwise use it verbatim. On the default branch, branch first. Commit, push, open the PR, reply with its URL, resolve.
- *Fix CI*: read the failing check's logs, fix the cause, commit and push to the same branch, reply with what changed, resolve.
- *Merge conflicts*: merge `origin/<base>`, resolve each conflict faithfully to both sides' intent, commit, push, and reply with how each one was resolved.

If commit signing fails or times out, tell the user to touch their security key and reply to retry; never disable signing.

In a sandbox you have no git or GitHub credentials. Go through the gateway, where each call may wait while the user approves it: `mcp__oriel-gw__oriel__commit`, `mcp__oriel-gw__oriel__push`, `mcp__oriel-gw__oriel__pull`, `mcp__oriel-gw__oriel__pr_create` (from the saved draft unless you pass a title and body) and `mcp__oriel-gw__oriel__ci` (`logs: true` for a failing check). If `pr_create` says the change isn't verified yet, ask the user to verify it; don't work round it.

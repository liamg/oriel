# Gitea, as a code host plugin

A worked example: Gitea added to Oriel in plain Lua, with no process of its own. Every call is an
`oriel.http.request` to the site's API with its token *named* (`auth = site.secret`); Oriel attaches the token, and
the plugin never sees it. `app/gitea_example_test.go` loads this directory against a fake Gitea and raises, warns,
merges and lists reviews through it, so the example stays in step with the API.

## What you get from it

- **Ship:** "Raise PR…", its checks, **Merge…** with the dialog that warns of unresolved review comments, changes
  asked for, a draft (a `WIP:` title) or a conflict.
- **Reviews:** the pull requests asking for your review, on the Reviews page and its pill.
- **Your own server:** a Gitea that isn't gitea.com is recognised by its probe (`/api/v1/version`), or you name it in
  `code.hosts`, and set up once (`code.setup`): its API URL and its own token, kept by hostname.
- **The token:** the one you set, else `$GITEA_TOKEN`, else Oriel asks, with a link to make one.

## Installing it

Add it to `[plugins]` in `settings.toml` as a local directory:

```toml
[plugins]
"example/gitea" = { path = "~/src/oriel/docs/examples/gitea" }
```

Oriel shows what it asks for (network to gitea.com, the `GITEA_TOKEN` secret) and installs it on your say.

## The `oriel.code.host` functions it implements

| Function | Gitea API |
| --- | --- |
| `requested(site)` | `GET /repos/issues/search?type=pulls&review_requested=true` |
| `lookup(site, url)` | `GET /repos/{repo}/pulls/{n}`, `…/files` |
| `checkout(site, url)` | the `pull/{n}/head` ref |
| `for_branch(repo, branch)` | `GET /repos/{repo}/pulls?state=all` |
| `create`, `edit`, `ready` | `POST …/pulls`, `PATCH …/pulls/{n}` (a draft is a `WIP:` title) |
| `request_reviewers` | `POST …/pulls/{n}/requested_reviewers` |
| `checks` | `GET …/commits/{sha}/statuses` |
| `merge_methods` | the repo's `allow_*` and `default_merge_style` |
| `blockers` | the PR's title and `mergeable`, its reviews and their unresolved comments |
| `merge` | `POST …/pulls/{n}/merge` with `head_commit_id`, so a push since you looked is refused |
| `threads(repo, pr)` | `GET …/pulls/{n}/reviews`, `…/reviews/{id}/comments`, `…/issues/{n}/comments`; `member` from `GET …/collaborators/{user}` |
| `reply(repo, pr, thread, body)` | `POST …/issues/{n}/comments`: Gitea can't reply inside a review comment's thread, so the reply quotes its line |

Leave any function out and what it does isn't offered. See `oriel.code.host` in `docs/api.md` for each one's shape.

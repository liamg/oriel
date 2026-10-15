# What crit does today (the behaviour to carry over)

Tickets that say "port from crit" or "crit behaviour preserved" mean what's written here. Source: `github.com/liamg/crit`
(paths below are in that repo). Read the code for the exact behaviour; this is the map.

## Processes (being replaced, see `architecture.md`)

- **Manager/dashboard** (`internal/manager`, `internal/dashboard`): the only listener. Serves the UI, merges worker state,
  pushes settings, hosts the assistant, and reverse-proxies terminals and remote session servers.
- **Worker** (`internal/worker`): one per machine. Dials the manager over pinned TLS, with yamux streams (`internal/wire`).
  Owns checkouts, worktrees, session servers, agent PTYs, quota, stats and agentbus.
- **Session server** (`internal/server`, `internal/review`): one per workspace. JSON API, SSE events, `/api/wait` long poll.
- **Sandbox** (`internal/sandbox`, `sandbox/runner`, `internal/container`): one container per workspace, mise toolchains
  read-only, and stripped Claude credentials. Git push and pull go through the gateway.
- **Egress proxy** (`internal/egress`): a CONNECT proxy with allow/deny/ask rules. "Ask" becomes a network-approval card.
- **Gateway** (`internal/gateway`, `internal/mcp`, `server/gateway.go`, `server/ghtools.go`): a stdio MCP bridge for commit,
  push, pull, fetch, pr_create, ci, gh_*, ssh_agent, request_host, add_project and the user's own MCP servers. Layered
  allow/ask/deny policy (`gateway/policy.go`).

## Agent integration (Claude)

- Launch: `claude [--model M] --plugin-dir <mod> [--resume <id> | /crit:<skill>]` in a PTY. `CRIT_SPAWNED=1`,
  `CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1`. Directories are pre-trusted and settings defaults merged (`claudecfg`). Sandboxed
  sessions get `--mcp-config <gateway> --strict-mcp-config`.
- Mod (`mod/crit/hooks/register.ts`):
  - loops `crit wait --push`, then `$.prompt.submit`; urgent comments or a stop call `$.turn.abort`;
  - `$.model.fork` serves btw, hunk explain and quiz;
  - `$.tool.register` adds the `mcp__crit__*` tools, each shelling out to the CLI;
  - permissions and questions go to the browser;
  - turn and subagent events and usage are reported;
  - `tool.check` denies edits to the original checkout and network git/gh commands.
- Transcript reading (`internal/transcript`), stats and cost (`internal/stats`), and quota from the client-side `claude -p "/usage"`
  probe (`internal/quota`).

## Features to keep (by area)

**Sessions and workspaces** (`internal/workspace`)
- 1..n repo worktrees on one branch under `~/.crit`.
- add-project and remove-project, base-branch picking, comment re-anchoring, Start next PR.
- Resume, restart, kill, drain before restart, stale-workspace cleanup, an outstanding-work warning, titles, a ticket per session,
  image paste.

**Kinds**
- diff, plan (`internal/plan`), new, tour (7 kinds plus export, `internal/tour`), job (`internal/job`), and review-PR with
  automatic AI review (`autoreview.go`).

**Threads**
- Line, range, file and whole-change comments; markdown; severity; read tracking; urgent; btw.
- Dismiss and reopen; "improve" and "detail" rewrites before forwarding.
- The slop gate (`internal/slop`).

**Explain**
- Hunk explain cards cached by content (`review/explain.go`), and the "Explain this change" walkthrough (`walkthrough.go`).

**Git and PR**
- The Git page (uncommitted, behind, unpushed, PR), sync and conflicts (`server/sync.go`), commits.
- The PR page: checks, an editable description from the template, raise-PR, the reviewer picker, previous PRs, merge (`internal/gh`).
- Commit and PR drafts written when the agent goes idle. CI-fix and conflict prompts. A security-key touch prompt.

**GitHub mirroring**
- Reviewer comments become `origin: github` threads. The agent drafts replies, and the user forwards them. Echo reconciliation (`review/github.go`).

**Trusted orgs**
- PR and comment text from outside the trusted orgs and users is redacted for the agent (`review/redact.go`).

**agentbus** (`internal/agentbus`, `worker/agentbus.go`). Ported as an external plugin, `github.com/liamg/oriel-agentbus` (PL-5), not part of Oriel.
- Question cards with an auto-answer at the deadline (read-only tools, labelled).
- Review-request cards (Ignore, Claim, Claim + Open), with the result reported back to the asker, including GitHub-submitted reviews (`ghreviewed.go`).
- Auto-approve rules (`internal/autoapprove`), presence (active, idle, DND), and a log.

**Linear** (`internal/linear`, `dash/linear.js`, `dashboard/linear*.go`)
- A list of assigned tickets.
- Start work from a ticket: the ticket, its comments and attachments become the plan prompt.
- Ticket state moves (to in progress on start, to in review on PR, to done on merge) and links back to the PR.
- "Continue" for a ticket that already has a session (crit: a workspace), instead of starting another.

**Slack** (`internal/slack`)
- A "PTAL" button posting a review request through an incoming webhook.

**PR lists** (`prmerge.go`, `worker/prlists.go`)
- Mine and review-requested, merged across workers.

**Assistant** (`internal/assistant`, `dashboard/assistant*.go`)
- Tools: my_prs, my_tickets, review_requests, past_sessions, new/start/open/resume/stop/rename_session, review_pr,
  request_review, run_job, mission_control, crit_settings, and web search.
- Confirmation cards.
- **Drop** the fun tools (cinema, music, room).

**Voice** (`internal/voice`, `dashboard/voice.go`, `piper.go`)
- Dictation with Ctrl+Shift+Space (whisper.cpp or an OpenAI-compatible endpoint), an optional wake word, spoken replies.

**Prompts page** (`internal/prompts/defaults`)
- 28 editable prompts, including Coding style (also written into the generated CLAUDE.md). Only overrides are stored.

**Status colours** (`server/status.go: statusGlyph`)
- The rules for needs you, permission, working, CI, reviewer and quiet. Oriel's colours differ (see `ui.md`), but the rules carry over.

**Telemetry**
- Usage windows and burn rate, per-session tally, stats, worker CPU and memory (`internal/sysload`), the log stream (`internal/logfile`).

**Self-update**
- `selfupdate.go`, `manager/updateall.go`.

## Not carried over

- The 3D room, scenes, sea and coast, black hole, lights, post-its, cinema/IPTV/YouTube/CCTV, Spotify and music, and chess puzzles.
- The calendar (`internal/ical`, `dashboard/maccal.go`: the next meeting from an iCal URL or macOS EventKit).
- The company feed (`dashboard/company.go`: org-wide PRs and Linear moves).
- `internal/media` and all of `web/static/room` apart from the omnibox, launch, assistant and panes logic, which are reimplemented natively.

# Configuration

Oriel keeps plain values in TOML and logic in Lua. The settings page (`:Settings`, ⌘,) and a hand edit change the
same file, and neither clobbers the other.

## Files

- `~/.config/oriel/settings.toml` is yours; `<repo>/.oriel/settings.toml` is a project's, trusted on first open.
- `~/.config/oriel/init.lua` holds keymaps, hooks, commands and anything computed. The same layout under
  `<repo>/.oriel/` gives per-project config.
- Only non-defaults are written, and a GUI edit touches only the line it changes: comments, key order and formatting
  survive. Resetting an option deletes its line.
- The files are watched: a hand edit applies at once. A parse error keeps the last good values and shows where it is.

```toml
# ~/.config/oriel/settings.toml
[editor]
tab_stop = 4

[diff]
layout = "unified"
```

## Precedence

Later layers win: built-in defaults, `~/.config/oriel/settings.toml`, `init.lua`, `<repo>/.oriel/settings.toml`, the
session, the file's own conventions (`.editorconfig`, `.gitattributes`, modelines when on), then the buffer and window
(`:set`). A value set in `init.lua` is shown read-only on the settings page, with a link to its line.

## Example init.lua

The Lua API is in [api.md](api.md). This sample is loaded by the test suite as written:

```lua
local L = oriel

-- options
L.option.relativenumber = true
L.buffer_option.tabstop = 4                       -- buffer-local
L.session_option.agent = "claude"                  -- session-local

-- keymaps, any mode, including panels' own modes
L.keymap.set("n", "<leader>rr", "agent.review")             -- a command
L.keymap.set({ "n", "x" }, "ga", function() L.thread.new() end)
L.keymap.set("requests", "x", "request.resolve")            -- keys inside the Requests panel
L.keymap.set("n", ",", "my.thing", { nowait = true })       -- don't wait for ",x" mappings (vim's <nowait>)

-- commands
L.command("Ship", function(args)
  L.cmd("git.commit", { draft = true })
  L.cmd("pr.raise", { verify = true })
end, { desc = "commit, then verify & raise" })

-- events: observe / veto / rewrite / decide
L.on("BufWritePre", { pattern = "*.go" }, function(ev) L.lsp.format(ev.buf) end)

L.on("Commit", function(ev)
  if not ev.message:match("^%a+[%(!:]") then
    return false, "not a conventional commit"   -- veto
  end
end)

L.on("CommentSend", function(ev)        -- rewrite before the agent sees it
  ev.body = ev.body:gsub("@t(%d+)", function(n) return L.linear.link("ENG-" .. n) end)
  return ev
end)

L.on("Permission", function(ev)         -- decide
  if ev.tool == "Bash" and ev.input:match("^go test ") then return "allow" end
end)

-- agent tools: offered to the session's agent over MCP
L.agent.tool{
  name = "staging_logs",
  desc = "Tail the staging logs for a service",
  params = { service = "string", lines = "number?" },
  run = function(p) return L.exec{ "kubectl", "logs", "-n", "staging", p.service, "--tail", p.lines or 200 } end,
}

-- prompt packs and session kinds
L.agent.explainer{ name = "local", order = 5, label = "a local model",   -- answers every "explain this" first
  byline = "written by a local model",                                  -- under a long answer: a walkthrough
  run = function(req) return my_model(req.subject .. "\n" .. req.context, req.depth, req.format) end }
L.agent.pack("hotfix", { prompt = L.read("~/.config/oriel/agents/hotfix.md"), tools = { "staging_logs" } })
L.session.kind("hotfix", { pack = "hotfix", gates = { "tests", "threads" }, can = { "ships", "owns-changes", "owns-branch" } })

-- views: statusline, HUD indicators, panels, all replaceable
L.statusline(function(w)
  return L.ui.row{
    L.ui.mode(),
    L.ui.text(w.session.branch, "dim"),
    L.ui.spacer(),
    L.ui.indicator{ label = "CTX", bar = w.session.ctx_frac },
  }
end)

L.hud.indicator("deploys", { slot = "titlebar.right", every = "30s" }, function()
  local n = L.json(L.http.get("https://deploy.internal/api/pending")).count
  return L.ui.indicator{ label = "DEPLOY", value = n, tone = n > 0 and "warn" or "ok" }
end)

L.view.override("requests", function(s)            -- replace a built-in panel
  return L.ui.list(s.threads:filter("open"):sort("age"), require("my.thread_row"))
end)

-- decorations
L.decor.provider("coverage", function(buf, top, bot)
  return L.coverage.lines(buf, top, bot):map(function(l)
    return { line = l.n, sign = l.hit and "▎" or "", hl = l.hit and "CovHit" or "CovMiss" }
  end)
end)

-- status colours are rules too
L.status.rule(function(s) if s.kind == "hotfix" and not s.pr_merged then return "red" end end)
```

## Option list

Every option Oriel registers. A test checks this table against the registry.

| Option | Default | Layer | Aliases |
|---|---|---|---|
| `editor.clipboard` | `system`: `y`, `d` and `p` use the system clipboard; `registers` keeps them in vim's registers | input | `clipboard` |
| `editor.shift_width` | 8 | input | `shiftwidth`, `sw` |
| `editor.tab_stop` | 8 | input | `tabstop`, `ts` |
| `editor.expand_tab` | false | input | `expandtab`, `et` |
| `editor.ignore_case` | false | input | `ignorecase`, `ic` |
| `editor.smart_case` | false | input | `smartcase`, `scs` |
| `editor.wrap_scan` | true | input | `wrapscan`, `ws` |
| `editor.wrap` | false: long lines run off the right and the window scrolls sideways (nvim's is on) | input | `wrap` |
| `editor.side_scroll` | 1 | input | `sidescroll`, `ss` |
| `editor.side_scroll_off` | 0 | input | `sidescrolloff`, `siso` |
| `editor.magic` | true | input | `magic` |
| `editor.report` | 2 | input | `report` |
| `editor.number_formats` | `bin,hex` | input | `nrformats`, `nf` |
| `editor.hidden` | true | input | `hidden`, `hid` |
| `editor.font_size` | 13.5 | app | |
| `editor.line_height` | 1.55: a multiple of the font size | app | |
| `editor.cursor_glide` | true: the cursor glides between places; typing, single steps and held keys still move it at once | app | |
| `markdown.remote_images` | true: markdown previews and threads load images from http(s) URLs; off, each is a chip with its address (no tracking pixels) | app | |
| `editor.end_of_line` | `keep`: the file's own; or `lf`, `crlf` | app | |
| `editor.charset` | `keep`; or `utf-8`, `utf-8-bom` | app | |
| `editor.trim_trailing_whitespace` | false | app | |
| `editor.insert_final_newline` | true | app | |
| `editor.max_line_length` | 0: none | app | |
| `editor.binary` | false | app | |
| `editor.modelines` | false | app | |
| `editor.threads` | `shown`: files open with their threads between the lines; `hidden`, behind gutter bubbles | app | |
| `files.generated` | false | app | |
| `git.gutter.base` | `scope`: the gutter follows the Explorer's scope, Uncommitted's against the last commit, else the branch's base; `branch`, where its branch left the base; `head`, the last commit; `index`, what's staged | app | |
| `git.blame.inline` | true: who last changed the cursor's line, after it | app | |
| `debug.inline_values` | true: while paused, the frame's variables after the lines that name them | app | |
| `diff.layout` | `split`: the review diff's old and new side by side; `unified`, one above the other | app | |
| `review.ai.provider` | `auto`: the session agent's; or `claude`, `codex` | app | |
| `review.ai.model` | `default`: the session agent's, or the picked provider's default; or a model's name | app | |
| `github.trusted_orgs` | `[]`: GitHub orgs whose members' review comments the agent may read; empty means the repository's owner | app | |
| `github.trusted_users` | `[]`: GitHub logins whose review comments the agent may read, beside the trusted orgs' members | app | |
| `review.severity_badges` | `true`: each comment a review session posts is headed with a badge of its severity | app | |
| `review.auto_rereview` | `true`: when the author pushes to a PR under review, fetch it and ask the agent to review what changed | app | |
| `review.start_reaction` | `👀`: the reaction a review session puts on its PR as it starts, where the host takes reactions; empty for none | app | |
| `reviews.poll` | `5m`: how often every review source is asked for the pull requests waiting on your review | app | |
| `reviews.count_teams` | `true`: a review asked of a team you're on counts toward the Reviews pill | app | |
| `reviews.notify` | `true`: a notice when someone asks for your review | app | |
| `reviews.max_age` | `30d`: review requests asked longer ago are left out of the page, the pill, the picker, the assistant's `review_requests` and the notices; `0` shows them all, and one with no time asked always shows | app | |
| `code.hosts` | `{}`: which code host a self-hosted server is, by hostname (`gitlab`, `github`, `azure`, `none`); filled in as Oriel finds out or asks; user and project | app | |
| `code.trusted_users` | `[]`: logins on a code host other than GitHub whose review comments the agent may read, beside the repo's members | app | |
| `code.api` | `{}`: a self-hosted server's API URL, by hostname, where it isn't the host's usual one | app | |
| `ship.merge_method` | `repo`: the repo's own default, else squash; or `squash`, `merge`, `rebase` when the repo allows it; user and project | app | |
| `session.archive_on_merge` | false: archive a session by itself once every one of its PRs is merged and its worktrees are clean; user and project | app | |
| `terminal.shell` | `$SHELL`: the program a new terminal runs, with its arguments; `$VARS` expand | app | |
| `plugins` | `{}`: the installed plugins by id, a table; user and project | app | |
| `registry.sources` | `["liamg/oriel-plugins"]`: the plugin registries in order; an id in more than one comes from the first | app | |
| `registry.keys` | `{}`: each registry's `ed25519:<hex>` key; the default registry's is built in | app | |
| `registry.updates` | `ask`: plugin updates that ask for no more; `auto` takes them, `manual` leaves them on the Plugins page | app | |
| `registry.trusted_hold` | 24h: how long a trusted plugin's new tag waits before it's offered | app | |
| `bundled.disabled` | `[]`: the plugins bundled with Oriel that are turned off, by id | app | |
| `editor.input` | `vim`: the input engine a buffer is edited with | app | |
| `lsp.start` | `project`: a project's servers start when it opens; `file`: when one of their files does | app | |
| `keymap.leader` | Space | app | |
| `keymap.timeout` | true | app | `timeout`, `to` |
| `keymap.timeoutlen` | 1s | app | `timeoutlen`, `tm` |
| `hooks.budget` | 2ms: how long a hook may take, on two calls running, before it's a warning | app | |
| `ui.font` | empty (Inter): the font the UI's text is set in, a family installed on the machine | app | |
| `ui.font_mono` | empty (JetBrains Mono): the fixed-width font, for the editor, terminals and labels set in it | app | |
| `ui.font_size` | 13: the UI's text size, which zooms the window | app | |
| `ui.statusline.font_size` | follows `ui.font_size` until set | app | |
| `ui.hover.font_size` | follows `ui.font_size` until set; hovers and signature help | app | |
| `ui.completion.font_size` | follows `ui.font_size` until set | app | |
| `ui.tooltip.font_size` | follows `ui.font_size` until set | app | |
| `sidebar.form` | auto: how a window's project sidebar starts, `wide`, `narrow`, or wide with 2+ projects; `«`/`»` set a window's own, kept with its layout | app | |
| `ui.reduced_motion` | false | app | |
| `ui.scrollbars` | auto | app | |
| `ui.theme` | `oriel` | app | |
| `ui.variant` | `system`: follows the OS; or `dark`, `light` | app | |
| `whichkey.enabled` | true | app | |
| `sandbox.default` | `sandbox`: a new session's agent runs in a container; or `host`, on this machine | app | |
| `repos.dir` | `~/src`: where clones and new repos go when nothing else says | app | |
| `repos.discover` | `true`: register the repos beside the ones you have, on start and when one is added | app | |
| `repos.tag.pull` | `true`: New tag pulls the branch first, fast-forward only | app | |
| `repos.tag.sign` | `auto`: sign new tags when a signing key is set up; or `always`, `never` | app | |
| `repos.tag.message` | `{tag}`: a new tag's message; `{tag}`, `{branch}`, `{repo}` expand | app | |
| `repos.tag.push` | `true`: New tag pushes the tag, and only it | app | |
| `sandbox.backend` | `docker`, `orbstack`, `colima` or `podman`: each agent runs in a container on that engine; or `seatbelt`, macOS's own sandbox with the host's toolchains. Only those installed are offered; the default is the first of those, in that order | app | |
| `sandbox.loopback` | `all`: the host's loopback ports a `seatbelt` sandbox may connect to; `ephemeral`, only 49152–65535 (servers on port 0, not services on fixed ports); or `none`, only its proxy | app | |
| `sandbox.image` | `base`: Oriel's base image; or an image's name; a project's applies to its repo | app | |
| `sandbox.claude_version` | empty: the host's Claude Code; a container sandbox downloads the version named (registered by the Claude plugin) | app | |
| `sandbox.env` | empty: the default host variables a sandbox inherits: `OTEL_*` (`sandbox.DefaultEnv`) and what the session's agent passes, Claude's own (`ANTHROPIC_*`, `CLAUDE_CODE_*`, `DISABLE_*` and its timeouts and token limits, less what a Claude Code session sets for itself) or Codex's (`OPENAI_*`, `CODEX_*`); the page offers the host's | app | |
| `sandbox.egress` | empty: network rules, `allow\|deny host[:port] [private]`; a project's add to it | app | |
| `sandbox.rules` | empty: gateway rules, `allow\|ask\|deny tool`; a project's add to it | app | |
| `sandbox.approval_timeout` | 10m: how long a held gateway call waits for you | app | |
| `sandbox.auto_allow_read_only` | false | app | |
| `sandbox.hidden_servers` | empty; the page offers the servers the gateway finds | app | |
| `sandbox.servers` | empty: extra MCP servers for the gateway, name → an https URL or a command line | app | |
| `sandbox.egress_proxy` | true: off allows every connection (still made from the host, so its VPN and tailnet reach the sandbox) | app | |
| `sandbox.memory` | `8g`; `none` for no cap | app | |
| `sandbox.pids_limit` | 4096; -1 for no cap | app | |
| `sandbox.go_cache_max` | `20g`: the cap on the sandboxes' shared Go build cache, trimmed least recently used first by the hourly sweep; `none` for no cap | app | |
| `usage.accounts` | empty: names for the accounts plans belong to, account identity → label ("Work", "Personal"); unnamed, an account shows its email or organisation | app | |
| `stats.split_sessions` | true: a multi-repo session's cost splits over its repos, each request charged to the repo it worked in; off, each repo is charged the whole session. The Stats page's toggle and the session tab's "count whole session in each" both set it | app | |
| `hud.hold` | alt: the modifier that pulls the HUD down while held on its own (alt, ctrl, shift, super, off) | app | |
| `hud.hold_delay` | 250: milliseconds the hold key is held alone before the HUD comes down, so a chord doesn't flash it | app | |
| `sessions.auto_name` | true: a small model (Claude's Haiku) names a session started from a request, and the request; off, the name is the request's first words | app | |
| `sessions.restart_on_exit` | true: the agent of an open session whose kind can `restarts-agent` (all built-ins but jobs) starts again on its conversation when it exits on its own; one that exits 3 times in 2 minutes stays stopped, with a notice to resume it | app | |
| `updates.mode` | `automatic`: updates as Oriel starts, and installs a release that comes out while it runs for the next restart; `ask` downloads it and offers it in a notice; `manual` only checks on `:UpdateCheck` | app | |
| `updates.channel` | `stable`; or `nightly`, pre-releases too | app | |
| `voice.dictation` | true: every long text field (`widget.Prose`) has a mic, and ⌘⇧Space (Ctrl⇧Space elsewhere) in one dictates; false takes the mics away | app | |
| `voice.model` | the Whisper model: `base.en`, `small.en`, `medium.en` or a path to a ggml model; `base.en` by default, downloaded in the background when Oriel starts with `voice.dictation` on (unless the OS says the network is metered) | app | |
| `voice.url` | an OpenAI-compatible transcription endpoint used instead of a local whisper-server; empty uses whisper-server | app | |
| `voice.recorder` | `oriel`: Oriel captures it (Core Audio on macOS, PulseAudio or PipeWire on Linux); or a program: `pw-record`, `parecord`, `arecord` (Linux), `rec` (sox), `ffmpeg` | app | |
| `voice.speech` | `system` (say, spd-say or espeak), `piper` or `off`: how replies are spoken | app | |
| `voice.piper_voice` | a Piper voice's name in `~/.oriel/piper`; empty uses the first there | app | |
| `assistant.provider` | `auto`: the first agent installed and signed in; or `claude`, `codex` | app | |
| `assistant.model` | `small`: the provider's small model (Claude's Haiku); `default`, its default; or a model's name | app | |
| `assistant.web` | true: the assistant may search the web (the provider's WebSearch and WebFetch) | app | |
| `assistant.idle` | 15: minutes of quiet before a conversation ends and the next starts afresh | app | |
| `debug.agent.read` | allow: agents may read the paused stack, frames and variables (allow, ask or off) | app | |
| `debug.agent.eval` | allow: agents may evaluate expressions in a paused frame | app | |
| `debug.agent.breakpoints` | ask: agents may set and remove breakpoints once you allow each on a permission card | app | |
| `debug.agent.step` | off: agents may not continue or step the debuggee | app | |

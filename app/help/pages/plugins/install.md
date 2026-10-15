---
id: plugins/install
covers: [app.overlay.plugins.consent]
related: [plugins/lua, plugins/api]
---

# Installing a plugin

A plugin says in its `oriel.toml` which capabilities it needs. Installing it shows every one of them beyond the
always-allowed UI, themes and notifications, each with Oriel's explanation and risk, and the author's reason in
italics, labelled as theirs. It's **all or nothing**: Install grants all of them, Cancel grants none. There's no
partial grant, so a plugin never runs half-working. Anything not declared is refused when the plugin tries it, with
an error naming the capability.

A plugin that needs only the always-allowed capabilities installs without asking. `⌘↵` installs, `esc` cancels.

## Updates {#updates}

An update that asks for more is the same choice again: the dialog lists the new version's capabilities with what's
new marked, and **Update** takes it, or **Stay** keeps the version you have. An update that asks for the same or
less applies without asking; what it no longer needs is listed.

## Grammar packs {#grammar-packs}

Languages beyond the built-in set come as grammar packs (`languages.install`), built by Oriel's CI from pinned
sources. Oriel installs only a pack whose every file matches the checksums it ships with, into
`~/.config/oriel/languages/<name>/`. A grammar runs in WASM with no host calls, so it asks for nothing.

## editor.read {#editor-read}

Read open buffers and the worktree files the session can see: your code. Medium risk.

## editor.write {#editor-write}

Edit buffers. Its edits are authored as `plugin:<id>`, visible in blame and the review ledger. Medium risk.

## input.observe {#input-observe}

See every key you press, not only its own mappings. **High risk**: this is what a keylogger needs.

## fs.read {#fs-read}

Read files outside the editor API, in the listed places: `worktree`, `config` or explicit paths. Medium risk.

## fs.write {#fs-write}

Write files in the listed places. High risk.

## net {#net}

HTTP(S) to the listed hosts only. Inside a session's sandbox the egress proxy holds it to them; outside one, the
OS sandbox can hold it only to their ports. Medium risk.

## secrets {#secrets}

Use a named secret from your keychain, bound to a host: Oriel adds it to requests to that host, and the plugin never
sees the value. High risk.

## exec {#exec}

Run the listed programs, as you. **High risk.**

## agent.read {#agent-read}

Read session transcripts and threads: everything you discuss with your agents. High risk.

## agent.prompt {#agent-prompt}

Send prompts and comments to a session's agent, which can steer it. **High risk.**

## agent.tools {#agent-tools}

Offer the listed tools to agents. Their output reaches the agent, so it's marked as plugin-provided in the
transcript. **High risk.**

## gate {#gate}

`veto` and `decide` hooks: block commits and answer permission prompts. **Critical**: answering permission prompts
automatically is privilege escalation. While granted, the HUD says how many plugins can answer prompts.

## session.control {#session-control}

Start, stop and resume sessions. Medium risk.

## git {#git}

Commit, push and open PRs through the gateway, still subject to its approvals. High risk.

## clipboard {#clipboard}

Read the clipboard. Medium risk.

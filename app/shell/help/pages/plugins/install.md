---
id: plugins/install
covers: [app.overlay.plugins.consent, app.overlay.plugins]
related: [plugins/lua, plugins/api]
---

# Installing a plugin

A plugin says in its `oriel.toml` which capabilities it needs. Installing it shows every one of them beyond the
always-allowed UI, themes and notifications, each with Oriel's explanation and risk, and the author's reason in
italics, labelled as theirs. It's **all or nothing**: Install grants all of them, Cancel grants none. There's no
partial grant, so a plugin never runs half-working. Anything not declared is refused when the plugin tries it, with
an error naming the capability.

A plugin that needs only the always-allowed capabilities installs without asking. `⌘↵` installs, `esc` cancels.

## Installing from settings.toml {#settings}

Plugins are listed in `settings.toml` under `[plugins]`, by id. Adding a line installs the plugin, and taking it out
uninstalls it: its files go, and its line leaves `oriel-lock.toml`.

```toml
[plugins]
"acme/jira"     = "^0.4"                                 # from the registry
"someone/thing" = { git = "github.com/someone/thing" }   # not in a registry: unverified
"me/wip"        = { path = "~/src/wip" }                  # a local directory
"acme/linear"   = { version = "^1", enabled = false }     # installed, not loaded
```

`oriel-lock.toml`, next to `settings.toml`, records what you accepted for each plugin: its exact commit and its
capabilities. A plugin the lockfile covers loads without asking, which is how your dotfiles work on a new machine.
One that asks for more stays off, with a card in the inbox that opens this screen. A project's
`.oriel/settings.toml` can list plugins too, once you trust the project.

## The Plugins page {#page}

`:Plugins` lists what's installed: the built-ins, the plugins bundled with Oriel, and everything in `[plugins]`, with what each contributes, the
capabilities it holds and how it runs. **Updates** lists what has a newer version or waits for your consent.
**Registry** browses the plugin registries; Install adds the plugin's line to `[plugins]`.

## Registries {#registries}

```toml
[registry]
sources      = ["liamg/oriel-plugins", "acme/oriel-plugins"]
keys         = { "acme/oriel-plugins" = "ed25519:…" }
updates      = "ask"     # auto | ask | manual
trusted_hold = "24h"
```

Each registry's index is signed, and Oriel refuses one that doesn't verify with its key. An id in more than one
registry comes from the first. A version a registry revokes is disabled, with a card saying why.

## Badges {#badges}

The badge says who has looked at the code, not what it may do: consent is the same for every badge.

- **reviewed**: this exact commit was reviewed in a registry pull request.
- **trusted**: the registry trusts its author; this version wasn't reviewed.
- **unverified**: a `git` or `path` source, in no registry. Nobody has reviewed this code.

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

## agent.provider {#agent-provider}

Be an agent sessions run (`oriel.agent.provider`), or serve others' (`oriel.agent.bridge`): the agent is given your
prompts and the session's tools, and acts in your workspaces. **Critical**. Outside a sandbox, each program it runs
needs `exec` too.

## gate {#gate}

`veto` and `decide` hooks: block commits and answer permission prompts. **Critical**: answering permission prompts
automatically is privilege escalation. While granted, the HUD says how many plugins can answer prompts.

## session.control {#session-control}

Start, stop and resume sessions. Medium risk.

## git {#git}

Commit, push and open PRs through the gateway, still subject to its approvals. High risk.

## clipboard {#clipboard}

Read the clipboard. Medium risk.

## Bundled plugins {#bundled}

Some plugins ship inside Oriel (Linear): they load at start with what their manifest declares, without
asking, because they come with Oriel. The page marks them **bundled**. **Disable** turns one off (it's listed in
`bundled.disabled`) and **Enable** turns it back on; they can't be uninstalled. Installing the same id from a
registry replaces the bundled copy while it's installed.

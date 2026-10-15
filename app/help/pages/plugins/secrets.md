---
id: plugins/secrets
covers: [secrets, Secret, secrets.set, secrets.delete, secrets.dialog.save, http.request]
related: [plugins/lua, git/ship]
---

# Secrets

A plugin that talks to a service names the secret it needs, such as `SLACK_WEBHOOK` or `LINEAR_TOKEN`, and its
manifest binds each to a host: `secrets = { SLACK_WEBHOOK = "hooks.slack.com" }`. The plugin never reads one. When it
makes a request with `oriel.http.request`, Oriel attaches the secret itself, and only if the request goes to that
host, over https: a token as `auth`, a webhook (a secret that's a whole URL) as `url_secret`. No answer or error the
plugin gets shows it, and the log masks it.

## Setting one {#set}

`:Secret NAME` (or `secrets.set {name}`) opens a dialog to paste its value into; the field shows dots. **Save**
(⌘↵) keeps it in Oriel's secrets file in the state directory, readable only by you, replacing one already set.
`secrets.delete {name}` removes it. A plugin's own command often opens the dialog for you: `slack.webhook` for the
Slack webhook.

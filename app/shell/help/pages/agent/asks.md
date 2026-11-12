---
id: agent/asks
related: [agent/console, plugins/hooks]
---

# Waiting on you

When an agent wants something only you can give, it waits for a card's answer. Cards show in the agent console where
they arose, and over the window for any session (with its name), whatever view is in front: the gateway's (run on
host…) at the top, the rest stacked at the bottom right; the inbox keeps them all. A permission is a prompt in the
middle of the window instead, for the session in front only, once you've stopped typing for a few seconds; a
session behind calls for you from its tab ([Prompts](shell/inbox#prompts)). A card that
comes while you're typing in a text field waits until you've stopped for a moment, so it doesn't cover what you're
writing or catch a key meant for it. A card at the top takes the keys as it shows, so its keys (`y`, `n`…) answer
it whatever had focus; for a moment as it appears they do nothing, and once it's answered the keys go back where they
were. A view opened after it takes them from it. A session's tab counts the cards waiting on it. A `Permission` hook can answer
before a card shows. A stacked card's **Console** (`g`, `agent.console.show`) brings its session to the front with
the agent console open, so you can read what led to the ask before you answer it; a permission's prompt steps aside
for it, and waits there to be answered.

## Permission

The agent wants to run a command or use a tool. **Allow once** (`y`) lets this call through; **Always in this
workspace** (`a`) also makes the rule shown at the right, so the same kind of call goes through from now on, after a
restart too: `Bash(go test:*)` for commands starting `go test`, `Edit` for edits; **Deny** (`n`) refuses it, and the
agent reads that as the tool's result. Whatever the agent, it never posts to a code review, and an edit of your own
checkout is refused before you're asked.

## Deny with a note

`⇧n` asks for a note before denying: why not, and what to do instead. The agent gets it with the denial.

## Network

The sandbox holds a connection to a host until you answer. **Allow host** allows that host from now on, **Allow
\*.domain** every host under its domain, **Once** this connection only; **Deny** refuses it, and the agent sees a
refused connection.

## Gateway

The sandbox's gateway holds a call that leaves it, such as a push or opening a PR, until you allow or deny it.

## Question

The agent asks something with options: click one to answer. Where it takes several, pick them and press
**Answer**; or type your own answer and press `⏎`.

## Login

The agent's login stopped working: an expired or revoked login, or none. Every session with that provider waits on
this card, and a notice says what to do. For a provider that signs in from Oriel (Codex), **Log in** (`l`) opens its
sign-in in your browser. Otherwise it opens the agent's own terminal with its login command typed (`/login` for
Claude); finish signing in there. A sandboxed agent never signs in in its own terminal, as its sign-in is a copy of
this machine's: **Log in** opens a terminal on this machine running the provider's sign-in (`claude auth login`,
`codex login`), and the sandboxes pick it up. Before asking, a sandboxed agent is started once again with this
machine's sign-in handed over afresh, as its copy may be one the machine has renewed since. An agent with no way to
sign in from here says what to run in a terminal. Once you're signed in the cards go by themselves, and the sessions
carry on; **Check again** (`c`) looks now.

## Terminal

The agent's own terminal shows a dialog that no hook passes on to Oriel, such as Claude's "Allow external CLAUDE.md
file imports?", and the agent waits until someone answers it there. The card shows what the dialog asks. **Go to
terminal** (`t`) opens the agent's raw terminal: answer the dialog with the arrow keys and `⏎`, or `Esc`. The card
goes once you do.

## Security key {#key}

A commit, push, fetch or rebase has waited five seconds: it's most likely waiting for a touch of your security key,
to sign or to reach the remote over ssh. Touch the key and the card goes once the operation finishes. **Cancel**
(`c`) stops the operation instead.

{{keys "ask.*"}}

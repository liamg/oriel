---
id: agent/asks
related: [agent/console, plugins/hooks]
---

# Waiting on you

When an agent wants something only you can give, it waits for a card's answer. Cards show in the agent console where
they arose, a [question](#question) in its thread, and the rest over the window for any session (with its name),
whatever view is in front: a security key's touch at the top, the rest stacked at the bottom right; the inbox keeps
them all. What holds the agent until you allow or deny it (a permission, a gateway's call such as a push or leaving
the sandbox, a connection the sandbox holds) is a prompt in the middle of the window instead, for the session in
front only, once you've stopped typing for a few seconds; a session behind calls for you from its tab
([Prompts](shell/inbox#prompts)). A card that
comes while you're typing in a text field waits until you've stopped for a moment, so it doesn't cover what you're
writing or catch a key meant for it. A card at the top takes the keys as it shows, so its keys (`y`, `n`…) answer
it whatever had focus; for a moment as it appears they do nothing, and once it's answered the keys go back where they
were. A view opened after it takes them from it. A session's tab counts the cards waiting on it. A `Permission` hook can answer
before a card shows. A stacked card's **Console** (`g`, `agent.console.show`) brings its session to the front with
the agent console open, so you can read what led to the ask before you answer it; a prompt steps aside
for it, and waits there to be answered.

## Permission

The agent wants to run a command or use a tool. **Allow once** (`y`) lets this call through; **Always in this
workspace** (`a`) also makes the rule shown at the right, so the same kind of call goes through from now on, after a
restart too: `Bash(go test:*)` for commands starting `go test`, `Edit` for edits; **Deny** (`n`) refuses it, and the
agent reads that as the tool's result. Whatever the agent, it never posts to a code review, and an edit of your own
checkout is refused before you're asked.

### MCP calls {#mcp}

Claude's every call to an MCP tool comes to Oriel, whatever mode it's in, sandboxed and in bypass too: a claude.ai
connector's (`mcp__claude_ai_…`) or one of your own servers'. Your rules and `Permission` hooks answer first, then
this card; **Always** makes a rule for that one tool. Oriel's own tools never ask, nor do the sandbox gateway's,
which holds its own calls ([Gateway](#gateway)). If Oriel can't be asked about a call, it's refused, never left to
Claude's mode. A tool your Claude settings deny is never offered, and on the host
one they allow goes through. An agent nobody watches, such as the [assistant](agent/assistant), asks nothing: what it
was allowed goes through and the rest is refused at once. Codex and other agents ask about an MCP call only when
their own gate does.

## Deny with a note

`⇧n` asks for a note before denying: why not, and what to do instead. The agent gets it with the denial.

## Network

The sandbox holds a connection to a host until you answer. **Allow host** allows that host from now on, **Allow
\*.domain** every host under its domain, **Once** this connection only; **Deny** refuses it, and the agent sees a
refused connection.

## Gateway {#gateway}

The sandbox's gateway holds a call that leaves it, such as a push or opening a PR, until you allow or deny it.

## Question

The agent asks something with options: click one to answer. Where it takes several, pick them and press
**Answer**; or type your own answer and press `⏎`.

A question is asked in a thread, and waits there rather than over the window: at the foot of the thread's card,
between the editor's or the diff's lines and in Requests, as well as in the agent console. The agent says which thread
with `mcp__oriel__ask`; a question tool of its own that names none (Claude's) asks in the thread it's on (the one it's
working on, else one it was handed), else in the session's request, else in a general thread opened for it, which
keeps your answer and is resolved once you give it. The inbox and the session's tab count it, and choosing it in the
Inbox panel opens its thread. It stays in its thread until it's answered, wherever you answer it, or the agent
withdraws it: resolving the thread meanwhile leaves it there. From its field, `⇧⇥` moves back onto its options and
`Space` picks one.

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

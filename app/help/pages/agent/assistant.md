---
id: agent/assistant
covers: [assistant.toggle, assistant.open, assistant.close, assistant.say, assistant.send, assistant.new, assistant.stop, assistant.answer, assistant.pick, assistant.undo, assistant.show, app.overlay.assistant]
related: [agent/voice, agent/launcher, agent/jobs, agent/prompts]
---

# The assistant

`⌘/` pulls the assistant's drawer down over whatever you're doing; `⌘/` again or `esc` puts it away, and the
conversation waits for you. Ask it "what needs me?", "review github.com/acme/api/pull/4410", "stop the rounding
session" or "run the deps sweep". It reads before it answers: your sessions and why each waits on you, your pull
requests and the reviews waiting on you, your jobs, repos, plan usage and machines, and whatever a plugin adds (Linear
tickets, mission control). It can search the web for news and the like (`assistant.web`).

## What it runs on {#model}

The assistant is an agent of its own, with no session: a hidden one on this machine's worker, started the way any
session's is (Claude Code in its terminal with Oriel's mod; never `claude -p`), on a small model (Claude's Haiku)
unless `assistant.provider` and `assistant.model` say otherwise. The chip in the drawer's header says which. It can't
edit code, run commands or post anywhere: every tool it has is one of Oriel's. What it's told is the **Assistant**
prompt on the Prompts page.

## Conversations {#conversation}

One conversation at a time. **New conversation** starts afresh, and so does the next thing you say after
`assistant.idle` minutes of quiet. Turns you type and turns you speak are marked as such for it, with the time.

## Its tools {#tools}

The chips over an answer are the tools it used for it. **What it can do** lists what it acts with: ✓ for what it just
does (opening or renaming a session, opening the launcher or the settings), **asks first** for what starts or stops an
agent. **Sources** are what its tools read, and how long ago it last read each.

A plugin offers it tools of its own with an agent tool `for` the assistant.

## Rows {#rows}

When an answer is a list of things to act on, each is a row: `⌘1`–`⌘9`, or a click, opens a row's session or link, or
asks about it.

## Confirmation cards {#confirm}

Starting a session, a review or a job, stopping a session, or messaging someone waits for your yes on a card that says
exactly what will happen: `y` or **Yes** does it, `n` or **No** doesn't. A card nobody answers in two minutes does
nothing. It never asks you to confirm in words.

## Undo {#undo}

What it did shows with a ✓, **Open** for what it made, and **Undo** with a countdown while it can be taken back: a
session it started is deleted, a rename goes back, a stopped session resumes. **Recent actions** lists the last few.

## The composer {#composer}

Type and press `⏎`. **dictate** (`⌃⇧Space`) types what you say into it. While it's answering, **Stop** (`⌘.`) ends
its turn. With `voice.wake` and `assistant.wake` on, what you say after the wake word goes to the assistant and its
answer is spoken.

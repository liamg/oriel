---
id: agent/assistant
covers: [assistant.toggle, assistant.open, assistant.close, assistant.say, assistant.send, assistant.new, assistant.stop, assistant.answer, assistant.pick, assistant.undo, assistant.show, app.overlay.assistant]
related: [agent/voice, agent/launcher, agent/jobs, agent/prompts]
---

# Ori, the assistant

Ori is Oriel's assistant: the logo's O come to life, with two caret eyes, at the foot of the rail. Click it, or press `⌘/`, and its
window floats over whatever you're doing; it stays on top until you minimise it with **–** or `esc`, and the
conversation waits for you. A number on Ori's face counts what it said while its window was away, and a dot lights
beside it. Drag the window by its title bar, resize it by its edges or bottom corners; it opens where you left it. It
doesn't take the keyboard: click the editor under it and type there.

Ask it "what needs me?", "review github.com/acme/api/pull/4410", "stop the rounding session", "run the deps sweep",
"where's that test I was working on?", "switch to the web project" or "how do I split the editor?". It reads before
it answers: your sessions and why each waits on you, what you changed and where you are, your pull requests and the
reviews waiting on you, your jobs, repos, plan usage and machines, these help pages, and whatever a plugin adds
(Linear tickets, say). It can search the web for news and the like (`assistant.web`).

It never waits on its own permission prompts or questions: nobody sees its agent's terminal. Its agent starts in a
mode that never asks (Claude Code's "don't ask"; Codex never asking, in a read-only sandbox; other agents' asks
refused as they come). It may use its Oriel tools and, with `assistant.web` on, the web search and fetch tools;
anything else it tries (a shell command, an edit) is refused at once with a reason, and so is a question it would put
to you, so it carries on and says in its answer what you should do. A dialog its agent's terminal opens is cancelled.
The only thing it waits for is your yes on a confirmation card, and only for two minutes.

## Steering the window {#steering}

It can bring a session or a project to the front, and show a file at a line in the editor. Ask for "that test I was
working on" and it finds the tests changed most recently, shows the likeliest, and asks whether that's it, with the
others as rows to pick from. Ask how to do something in Oriel and it searches these pages, answers from them, and
opens the page when you want to read it.

## What it runs on {#model}

The assistant is an agent of its own, with no session: a hidden one, started the way any
session's is (Claude Code in its terminal with Oriel's mod; never `claude -p`), on a small model (Claude's Haiku)
unless `assistant.provider` and `assistant.model` say otherwise. The chip in its title bar says which. It can't
edit code, run commands or post anywhere: every tool it has is one of Oriel's. What it's told is the **Assistant**
prompt on the Prompts page.

## Conversations {#conversation}

One conversation at a time. **New** in its title bar starts afresh, and so does the next thing you say after
`assistant.idle` minutes of quiet. Turns you type and turns you speak are marked as such for it, with the time.

## Its tools {#tools}

The chips over an answer are the tools it used for it. Drag the window wide and a side comes beside the
conversation: **What it can do** lists what it acts with, ✓ for what it just does (opening or renaming a session, opening the launcher or the settings), **asks first** for what starts or stops an
agent. **Sources** are what its tools read, and how long ago it last read each.

A plugin offers it tools of its own with an agent tool `for` the assistant.

## Rows {#rows}

When an answer is a list of things to act on, each is a row: `⌘1`–`⌘9`, or a click, opens a row's session, link or
file, or asks about it.

## Confirmation cards {#confirm}

Starting a session, a review or a job, stopping a session, or messaging someone waits for your yes on a card that says
exactly what will happen: `y` or **Yes** does it, `n` or **No** doesn't. A card nobody answers in two minutes does
nothing. It never asks you to confirm in words.

## Undo {#undo}

What it did shows with a ✓, **Open** for what it made, and **Undo** with a countdown while it can be taken back: a
session it started is deleted, a rename goes back, a stopped session resumes. **Recent actions** lists the last few.

## The composer {#composer}

Type and press `⏎`, or click the mic to dictate. While it's answering, **Stop** (`⌘.`) ends
its turn.

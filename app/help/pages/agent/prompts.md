---
id: agent/prompts
covers: [Prompts, prompts.open, prompts.set, prompts.reset, prompts.save, prompts.edit, app.overlay.prompts]
related: [agent/launcher, agent/voice]
---

# Prompts

What Oriel tells its agents, as text you can change: your coding style, each kind of session's prompt pack, and the
parts the packs share. Only what you change is kept, a file each in your config directory, so a new Oriel still
improves the prompts you left alone.

## The Prompts page {#page}

`:Prompts`, **Prompts** on the start view, or the omnibox: the prompts on the left by group, the selected one on the
right. `esc` or **Close** closes it.

## The list {#list}

**Style** (the coding style), **Session packs** (one per kind of session, and the AI review's), **Pack parts**
(the sections packs pull in with an `<!-- include: name -->` line) and **Tour kinds** (how a tour's agent shapes each
kind of tour). `j`/`k` move. A prompt you changed says
**yours**.

## A prompt {#prompt}

Its name, when it's used and its text. Edit the text and **Save** (`⌘S`); saving the default, or nothing, puts it
back to the default, as **Reset to default** does. **Open in editor** (`e`) opens its file, made from the default
the first time: `prompts/style.code.md`, `prompts/tour.<kind>.md`, `agents/<kind>.md` or `agents/parts/<part>.md` in
your config directory.
A hand-written `agents/<kind>.md` is the same override.

Sessions get your prompts whenever one changes, so a session started after a save uses them. The coding style is also written into the `CLAUDE.md` and `AGENTS.md` Oriel generates at a
workspace's root, beside the list of its projects, so an agent picks it up however it was started. `{{style}}` in
any prompt is replaced with it.

---
id: agent/tour
covers: [tour, tour.open, tour.step, tour.scroll, tour.open-quote, tour.ask, tour.export, tour.close]
related: [agent/launcher, agent/requests, agent/plan]
---

# Tours

A tour session answers a question ("how does authentication work?") with a guided tour through the real code, one
step at a time, across every repository it touches. Start one from the launcher's **Tour** mode and pick its kind:
**walkthrough** (how the code works), **onboarding** (a whole codebase), **archaeology** (how it got this way),
**showcase** (what it does, from outside), **demo** (built and run for real), **incident** (symptom to cause) or
**feasibility** (how hard would X be). The kind steers what the agent researches and how it shapes the steps; the
format is the same for all of them.

The worktrees are pinned and read-only: a tour explains code and never changes it. The reader opens by itself when a
tour session comes to the front; `tour.open` opens it again.

## Steps

At the left: the kind, the question, the repos it reads and the commit they're pinned at, how much you've read, and
the steps. A step you've read has a ✓. Steps appear as the agent writes them, so you can read step 1 while step 6 is
being written. Move with `h` and `l` (or ← →, or **Prev** and **Next**), and scroll with `j` and `k`.

## Quotes and notes {#quotes}

A step is prose, and code quoted from its repository: the quote's header names the repo, the path and the lines,
and the lines carry the file's own numbers, read from the worktree rather than typed by the agent. The lines that
matter are lit, and notes are pinned under the lines they're about. A long quote shows only the lines around its
marks; **… n more lines** shows the rest. **open ↗**, or `gd`, opens the quote in the editor at its lines. A quote
`@` another revision shows the code as it was then.

A step can also have an example (code from nowhere), a recorded command and what it printed, a screenshot with
numbered pins, boxes and arrows over it, and diagrams, which show at the right as their mermaid source.

## Asking about a step {#ask}

**Ask about this step** at the right, or `a`, asks the agent about the step you're on: in its open thread, or a new
one on the step's file, so the agent knows where you are. Ask it a question, or to rework the step. Its answer shows
there too.

## Export {#export}

**Export as HTML** writes `tour.html` at the tour's root: the whole tour in one self-contained file, with no fetches
and nothing external, read a step at a time with a Present button for full screen. It then asks the agent to publish
it as an artifact; the link shows under the button once it has. `tour.export` with `format: "md"` writes `tour.md`
instead, with every quote resolved to its code.

{{keys "tour.*"}}

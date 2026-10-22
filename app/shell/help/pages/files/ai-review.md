---
id: files/ai-review
covers: [diff.right.50-ai-review]
related: [files/diff, agent/requests]
---

# AI review

A second opinion on the session's change from another model: by default a different provider from the agent that
wrote it (Codex reviews Claude's work, and the other way round), or the same provider on another model when only one
is installed. The reviewer reads the change and the code around it and changes nothing; whatever else it asks to do
is refused. Its findings show in the column at the diff's right, worst first.

## Reviewer {#reviewer}

The header names the reviewer and the commit the change was read from. **Review changes** runs a review
(`review.ai.run`); once there's been one, **Re-review changes** reads only what changed since, committed or not, and
tells the reviewer what it found before. The foot says whether the reviewer is a different model from the author.
`review.ai.provider` and `review.ai.model` pick the reviewer yourself. A review session (a pull request) is reviewed
as it starts.

## Severity {#severity}

Each finding is critical, high, med(ium), low or a nit. The chips count the findings at each level that aren't
dismissed; clicking one shows only those, and clicking it again shows them all.

## Findings {#findings}

Each card has its severity and category, a title, where it is (a click shows that line in the diff) and why it
matters.

## Actions {#actions}

**→ Thread** hands the finding to the session's agent as a thread on its line, asking whether it's right before it
changes anything; **Fix** asks it to fix it. Either way the card then links to that thread. A finding about a repo
the session hasn't got offers **Add project** instead.

## Dismiss {#dismiss}

**Dismiss** sets a finding aside; "n dismissed" shows them again, with **Restore**. A dismissed finding doesn't hold
up the AI review check before the change ships.

## Prompt {#prompt}

**Prompt…** opens the reviewer's instructions, your `agents/ai-review.md` in Oriel's config directory, made from the
built-in one the first time. The next review uses what you saved.

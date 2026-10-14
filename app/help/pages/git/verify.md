---
id: git/verify
covers: [verify.open, verify.send, verify.skip, verify.again, verify.continue, verify.open-file, verify.close, review.verify_before_pr]
related: [git/ship, agent/console]
---

# Verify before raising

Before a pull request goes out, the session's agent asks you about the change you're putting your name on: four short
questions, one at a time, in a chat over the Raise PR tab. It asks from a fork of its own context, with no tools and
outside its conversation, so nothing in the session changes. **Push + raise PR** (or **Push + raise all**) opens it
and waits for it; once it's through, the raise goes on.

Type an answer and send it with **⌘↵**. Each gets a verdict: **✓** right, **◐** partly, **✕** wrong. The progress
marks at the top show each in its colour.

## The file it points at {#file}

A partial or wrong answer points at the file (and line) to read. Click it, or press **⌥O** to open the file it's
asking about; the quiz waits where it was, and comes back when you raise again.

## Reviewer notes {#notes}

When every answer is right or partly right, the quiz has passed. **Add the answers to the PR as reviewer notes** puts
the questions, your answers and their verdicts in the PR's description (on by default); **Raise PR** goes on with the
raise. A wrong answer means it hasn't passed: **Ask again** asks new questions.

## Skipping {#skip}

**Skip verification (logged)** lets the raise through without the quiz. The skip is recorded in the session and its
log, and the "Verify quiz" gate on the Ship page says so.

## When it runs

`review.verify_before_pr` is `work` by default: on for work and plan sessions. `on` asks in every session, `off`
never. A repo sets its own in `.oriel/settings.toml`:

```toml
[review]
verify_before_pr = "off"
```

{{keys "verify.*"}}

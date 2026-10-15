---
id: git/verify
covers: [verify.open, verify.send, verify.again, verify.open-file, verify.close]
related: [git/ship, agent/console]
---

# Verify the change

When you want to check you understand a change before you put your name on it, the session's agent asks you about
it: four short questions, one at a time, in a chat over the page. It asks from a fork of its own context, with no
tools and outside its conversation, so nothing in the session changes. It's optional: start it with **Verify…** on
the "Verify quiz" row of "before this ships" (the Ship page or the Raise PR tab), or `verify.open`. A raise never
waits for it.

Type an answer and send it with **⌘↵**. Each gets a verdict: **✓** right, **◐** partly, **✕** wrong. The progress
marks at the top show each in its colour.

## The file it points at {#file}

A partial or wrong answer points at the file (and line) to read. Click it, or press **⌥O** to open the file it's
asking about; the quiz waits where it was, and **Resume** on its row brings it back.

## Reviewer notes {#notes}

When every answer is right or partly right, the quiz has passed. **Add the answers to the PR as reviewer notes** puts
the questions, your answers and their verdicts in the description of the PR you raise next (on by default). A wrong
answer means it hasn't passed: **Ask again** asks new questions, or **Again** on its row.

{{keys "verify.*"}}

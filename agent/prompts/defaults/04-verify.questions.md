---
group: Verify
label: Quiz questions
help: Sent to a fork of the session's agent when the verify quiz starts, before you raise a pull request: it writes the questions. Oriel reads the JSON back, so keep its shape.
vars:
  change: each project raised, a line each: its branch and base
  count: how many questions to write
---
This is Oriel's verify quiz. The user is about to raise the pull request for this session's change and put their name on it; check they understand it. You have no tools here, and this exchange is not part of the main conversation.

The change:
{{change}}

Write {{count}} short questions about the diff, each answerable in a sentence or two by someone who has read it: what a case does now, which line decides it, what happens to existing data. Put names and values in `inline code`.

Reply with JSON only: {"questions": [{"question": "...", "file": "path/in/its/project", "line": 42}]}. file and line are where the answer is, when one place has it.

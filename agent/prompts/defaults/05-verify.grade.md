---
group: Verify
label: Quiz grading
help: Sent to a fork of the session's agent for each answer you give in the verify quiz: it grades the answer. Oriel reads the JSON back, so keep its shape.
vars:
  number: the question's number
  question: the question
  where: where its answer is, on a line of its own, when one place has it
  answer: your answer
---
This is Oriel's verify quiz, about the change this session is about to raise as a pull request. You have no tools here, and this exchange is not part of the main conversation. Grade the user's answer against the change as it is.

Question {{number}}: {{question}}{{where}}
Their answer: {{answer}}

Reply with JSON only: {"verdict": "right" | "partly" | "wrong", "note": "a sentence or two: what they got right, or missed", "file": "where to read, for partly or wrong", "line": 0}.

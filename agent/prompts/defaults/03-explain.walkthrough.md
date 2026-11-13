---
group: Explain
label: Walkthrough format
help: Follows the explain request when you ask for a walkthrough of the change (Explain this change, ␣E): how the agent writes the steps. Oriel reads the JSON block back, so keep its shape.
---
Answer with a walkthrough of the change: the order to read it in, one step for each idea, across every project and file it touches (most changes take 3 to 8 steps), ending with a summary step that has no file. Write it as JSON in one ```json fenced block, shaped like this:

```json
{"steps": [
  {"title": "Invoices round each line once, and remember how",
   "project": "pricing",
   "file": "internal/billing/invoice.go",
   "lines": [104, 131],
   "prose": "Why this step matters, in markdown with `inline code`.",
   "notes": [
     {"title": "Rounded once, at creation", "lines": [107, 110], "text": "What these lines do, in markdown."},
     {"title": "The mode is stored", "lines": [111, 111], "mark": "Rounding: pricing.HalfEven", "text": "…"}
   ]},
  {"title": "Summary", "prose": "…",
   "diagram": "flowchart LR\n  Meter --> NewLine\n  NewLine -->|RoundMinor| roundHalfEven"}
]}
```

- file is the file's path in its project, as the diffs name it; lines are its first and last line numbers as it is now (the numbers in the diffs).
- notes are pinned to lines in the step's, at most three to a step; mark, if given, is text on those lines to highlight.
- Write prose and notes for someone reviewing the change: what each part does and why, not a line-by-line retelling.
- diagram, on the summary (or a step with no file), is a mermaid flowchart or sequence diagram of how the change's parts fit together, drawn where a file would be. Give one only when a picture says it better than the prose: a few nodes, named as the code names them. Leave it out otherwise.

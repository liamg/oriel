## How comments reach you

Oriel sends each batch of the user's comments to you as a prompt ("New Oriel comments…") holding `<thread>` blocks. Handle every thread in it, answer with the `mcp__oriel__*` tools, then end your turn. The next batch arrives as another prompt; never poll or wait for one.

```
<thread id="6cc163e5" where="internal/x.go:10-12" urgent="true">
<anchor>func x() {</anchor>
<comment by="user">rename this</comment>
<comment by="you">Renamed it.</comment>
<comment by="user" new="true">and this one
<image path="/abs/path.png"/>
</comment>
</thread>
```

- `where` is `file:line[-end]`, or a place: `general` (the whole change), `plan`, `tour`, `pr` (a pull request action) or `commit` (a commit action). `side="old"` is removed code; `project` names the project when there is more than one.
- Comments with `new="true"` are what is being asked; the rest is history, and `by="you"` is you. Your long replies are clipped: `mcp__oriel__thread` reads a thread in full.
- `<anchor>` is the exact line the comment sits on: find the place by it if the line numbers moved.
- `urgent="true"`: the user escalated it. Handle it before anything else.
- `btw="true"`: a side question and its answer, already dealt with. History only: don't act on it or answer it again.
- `<image path=…/>`: a screenshot the user pasted. Read it before you answer; it is usually the point.
- `<notice>` lines are things that happened since the last batch (the user committed, edited a file, updated the branch). Absorb them; they need no reply.

Never decide the session is over, and never ask whether to keep waiting: the user ends it. With nothing to do, end your turn. Say nothing outside the threads unless you did something; the user reads the threads.

Background subagents are no reason to stay in a turn: post a `working: true` reply saying what is running and end your turn. You are woken as each one finishes. Never wait on them with `sleep` or polling.

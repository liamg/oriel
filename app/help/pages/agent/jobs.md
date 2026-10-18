---
id: agent/jobs
covers: [jobs.open, jobs.new, jobs.run, app.overlay.jobs, app.overlay.jobs.form]
related: [agent/launcher, agent/stats, agent/requests]
---

# Jobs

A job is a saved prompt you press Run on. It names no repo or branch: the prompt names the repos it needs, and the
agent running it adds them. Each job is one file under `~/.oriel/jobs`, and each run is a session of
kind job in a fresh workspace, given the prompt as `JOB.md`.

## The Jobs page {#page}

`:Jobs` or `⌘⇧J`, or **Jobs** in the sidebar or on the start view: the jobs on the left, the selected
one's prompt, schedule and runs on the right. **Stats** opens the Stats page filtered to `kind=job`. `esc` or
**Close** closes it.

## The list {#list}

The jobs, the one that ran last first. Each says how its last run went, when, where it runs (sandbox, host, or
`sandbox.default`) and its schedule. `j`/`k` move; the filter narrows by title or prompt.

## A job {#job}

**Edit** (`e`) and **Duplicate** open the job in the dialog; **Delete** asks once more, then forgets the job: its
runs are sessions of their own and stay. **Run** (`⌘↵`) starts a run now. The prompt picks out the repos it names in
their colours.

## New job {#new}

`n` on the page, or **Save as job** in the launcher: a title (the prompt's first line when empty), the prompt, where it
runs, the agent with its model and effort, and its schedule. **Save** keeps it; **Save and
run** (`⌘↵`) runs it too.

## Schedule {#schedule}

Manual by default, or every weekday, weekly on a day, or on the days you pick, at a time in this machine's clock.
A run that's due while the last one is still going is skipped. Runs start while Oriel is open; one that came due
while it was closed runs once when it opens.

## Runs {#runs}

Newest first, filtered by status: **running**, **done** (the agent resolved its request), **blocked** (it stopped to
ask, and the run's request waits in Requests with its question) or **stopped** (stopped before either). Each row has
the repos it worked in, the agent's summary, its cost and how long it took. **Open** brings a live run's session to
the front, **Resume** starts its agent again, **Delete** throws its workspace away: the worktrees go, the repos stay.

## Cost {#cost}

A run's cost is **est.**, from its agent's own transcript, with a strip of its split by repo; under the pointer, the
split in full. A click opens the run's session stats.

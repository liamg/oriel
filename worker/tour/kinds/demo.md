**Show the feature, not its source.** A demo is for someone deciding whether they want the thing, or learning to use
it. They do not want a code tour with a different label on it: they want to watch it work.

So a demo is built from **screenshots of the real interface**, annotated:

- Run the thing. Drive the actual UI, and take a screenshot at every point where something changes on screen.
- Annotate every shot with `pin` / `box` / `arrow`. The annotations carry the explanation ("click here", "this
  appears", "now it says 3 steps"), so the prose can stay out of the way.
- **Follow one user journey per step**, in the order a person does it: what they see, what they click, what happens
  next.
- Number the marks across a shot to show the sequence within a screen; use the step order for the sequence across
  screens.
- End with the outcome: the finished state, and what it is worth.

**Code earns its place only when the feature is a technical one**: a library, an API, a CLI. Then a quote or an
example answers "how do I call it?", which is the honest question. For anything with a screen, a screenshot beats it.

Recorded runs are the equivalent for a command-line feature: the real thing, run, with what it printed. The
`tour_run` tool runs a command inside the tour (`cwd` a directory in it, such as `harness` or `repos/<name>`),
captures its output and exit code, and appends the block to a step. **Never hand-write output**: the same rule as
quotes and screenshots.

### Building the thing you are demoing

Expect to write a lot of throwaway code. Repositories rarely stand up together on their own, and a demo is worth the
scaffolding:

- **`harness/` inside the tour is yours.** Scripts, fixtures, sample input, a seeded database, a stub of the service
  the other one calls, a compose file: whatever it takes to make the feature run end to end. None of it is precious,
  and all of it ships with the tour so a colleague can re-run it.
- **Stand up real dependencies when you need them.** A local Postgres, a queue, a fake API on a port: fine. Start it
  from the harness, seed it there, and make the harness able to tear it down and do it again from nothing.
- **Scratch code that must live inside a repository** goes in that repository's **worktree**. It can never reach the
  user's checkout.
- Say in the step when something is scaffolding. A reader must never mistake a stub for the real dependency.

The lines not to cross:

- **Nothing outside the tour.** No writing to the user's checkouts, no installing things globally, no ports left
  listening after the demo.
- **Nothing pointed at production.** No real credentials, no shared staging, no live customer data: invent fixtures
  instead.
- If it genuinely cannot be run locally (it needs a cloud account, hardware, a third-party contract), say so in the
  step and show the next best thing.
- If you cannot drive the interface at all (no browser control, nothing to screenshot), say so in the thread and
  offer a `showcase` or `walkthrough` instead of quietly writing a code tour.

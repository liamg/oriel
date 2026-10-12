# Testing: test-first, with sources we can check

Oriel is built test-first. And because agents write most of it, every test of behaviour has to show
**where the expected behaviour comes from**, in a form a machine can check. "I believe `cw` works
like this" isn't a source.

## Rules

1. **Red first.** A behaviour change starts as a failing test. A PR touching `vim`, `text`, `term`,
   `lsp` or `dap` without a new or changed test case is rejected by CI (with an explicit
   `no-test:` reason in the PR as the escape hatch, reviewed by you).
2. **Every behaviour test cites a source**: a `ref` to a vendored spec, plus a verbatim `quote`.
3. **References are verified, not trusted.** `refcheck` (our tool, run in CI) checks that each `ref` resolves to a
   real tag or section in the **pinned, vendored** copy of the doc, and that the `quote` appears there verbatim.
   A made-up reference or misquoted sentence fails the build.
4. **Where an oracle exists, the expectation is checked against it.** For vim that's headless neovim.
   An expectation that disagrees with the oracle fails, unless it's marked `deviation:` with a reason you've approved.
5. **Stdlib `testing` only** (see `deps.md`). No assertion libraries. Golden files and table tests.

## Vendored specs (`spec/`, pinned)

| Area | Source | Pinned as |
|---|---|---|
| vim behaviour | Vim `runtime/doc/*.txt` (`:help`), plus Neovim's where it deliberately differs | a tag, e.g. vim `v9.1.xxxx`, nvim `v0.11.x` |
| vim regex | `pattern.txt` | same |
| LSP | the LSP 3.17 specification (markdown source) | commit |
| DAP | the Debug Adapter Protocol spec + JSON schema | commit |
| terminal | xterm `ctlseqs`, ECMA-48, kitty keyboard and graphics protocol docs, Ghostty's VT docs | commit |
| text | Unicode UAX #29 (graphemes), #14 (line breaks), #11 (width), plus the UCD data files | Unicode version |
| colour | CSS Color 4 (OKLCH), APCA | commit |
| git | `git-*` man pages for porcelain output we parse | git version |

- `spec/` is data: no code from it is linked. Updating a pin is a PR that re-runs every referencing test.
- `refcheck` understands each format: vim `*tags*`, markdown headings, RFC section numbers, man page sections.

## The vim engine: keystroke tests

Cases live as plain-text golden files, one behaviour per case:

```
# testdata/motion/w.vt
=== w moves to the start of the next word
ref:    motion.txt *w*
quote:  "[count] words forward."
given:  foo |bar baz
keys:   w
expect: foo bar |baz

=== 2w with a count
ref:    motion.txt *w*
given:  |foo bar baz
keys:   2w
expect: foo bar |baz

=== cw on a word acts like ce
ref:    change.txt *cw*
quote:  "Special case: When the cursor is in a word, \"cw\" and \"cW\" do not include the white space after a word"
given:  |foo bar
keys:   cwxy<Esc>
expect: x|y bar
mode:   normal
regs:   "" = "foo"   "1 = (unset)   "- = "foo"
```

- `|` is the cursor. `[`…`]` mark a visual selection, and `^` the visual start when it matters.
- Optional checks: `mode`, `regs`, `marks`, `jumps`, `undo` (the text after `u`), `dot` (the text after `.`), `msg` (the echo line).
- The **runner** (`vim/vimtest`) feeds `keys` into our engine and compares every declared field.
- **Oracle mode** (`go test ./vim/... -oracle`): the same case runs in `nvim --headless --clean` over its
  RPC API. Buffer, cursor, mode, registers and marks are read back and compared with `expect`.
  - If **our expectation disagrees with nvim**, the case fails. The test was wrong, not just the code.
  - If vim and nvim differ from each other, the case says which it follows (`follows: vim`), with the `:help` reference that shows the difference.
- CI runs both modes. Oracle mode needs nvim, which is pinned in the CI image.

### Workflow for an agent adding a feature

1. Find the `:help` tags for the feature. Write cases with `ref` and `quote`, edges included (counts, empty lines, end of file, multibyte, wrap).
2. `refcheck` passes, so the sources are real.
3. `-oracle` passes, so the expectations match nvim.
4. `go test` fails, so the feature is missing (red).
5. Implement until it's green. Each commit stays green.
6. The compatibility table (`editor.md`) regenerates from which tags have passing cases.

### Beyond hand-written cases

- **Mined cases**: vim's own `src/testdir/test_*.vim` (normal, textobjects, visual, change, undo, put, registers…)
  are translated into `.vt` cases by a converter. Each case keeps a `ref` to the original test function.
- **Differential fuzzing**: random keystroke sequences (weighted towards real usage) run against both
  engines. Any divergence is minimised (delta debugging) into a small `.vt` case and saved as a regression.
  It runs nightly and for a time-boxed slice on each PR.
- **Invariants (property tests)**: `u` after any sequence restores the exact buffer; anchors stay in range;
  the rope matches a naive `[]byte` model; there are no invalid UTF-8 splits; dot-repeat equals replaying the change.

## Everything else

| Module | Tests | Source of truth |
|---|---|---|
| `text` | property tests against a naive model; UTF-16/grapheme offsets | UAX #29, LSP "position encoding" |
| `syntax` | highlight snapshots per language (query captures over fixture files); unit/text-object ranges | tree-sitter query docs; grammar test corpora |
| `lsp` | recorded JSON-RPC transcripts replayed against a fake server; each message shape cites a spec section | LSP 3.17 |
| `dap` | recorded sessions with real dlv and debugpy, replayed; live runs in CI | DAP spec |
| `term` | `vttest`/esctest-style scripts: bytes in, expected grid out; every case cites a `ctlseqs` entry | xterm ctlseqs, ECMA-48, kitty docs |
| `render`, `ui` | the headless backend records display lists, compared as snapshots; plus golden PNGs with a perceptual-diff threshold | the mockups (`ui.md`) as reference |
| `theme` | token resolution tables; every built-in theme rendered on the fixture workspace; contrast lint | CSS Color 4, APCA |
| `agent/*` | the provider conformance suite (start, send, tool call, permission, interrupt, fork, resume) against a **fake `claude`/`codex` binary** that replays recorded hook and transcript traffic; plus a slow live suite run on demand | recorded sessions from real CLIs, re-recorded when versions bump |
| `wire`, `worker` | in-process worker tests; chaos tests (drop the connection mid-stream, reconnect, compare state) | – |
| `gitx` | fixture repos built by script; porcelain output parsing | git man pages |
| perf | benchmarks for key-to-photon latency, scroll frame time and parse throughput; CI fails on a >10% regression | budgets in `rendering.md` |

## Example: a test that can't hallucinate

```go
func TestMotions(t *testing.T) {
    for _, c := range vimtest.Load(t, "testdata/motion/*.vt") {
        t.Run(c.Name, func(t *testing.T) {
            c.RequireRef(t)                 // has ref + resolvable tag; quote verified by refcheck
            got := vimtest.Run(engine.New(), c.Given, c.Keys)
            c.Compare(t, got)               // buffer, cursor, mode, regs… whatever the case declares
            if *oracle {
                c.Compare(t, nvimOracle.Run(c.Given, c.Keys)) // the expectation itself must match nvim
            }
        })
    }
}
```

## For agents (also in the repo's CLAUDE.md / AGENTS.md)

- Don't write an expected value from memory. Find it in `spec/`, quote it, or get it from the oracle.
- If the docs are ambiguous, run the oracle and cite the case as `ref: oracle` with the nvim version. Never guess.
- Never weaken or delete a failing test to get to green. Fix the code, or raise a `deviation:` for review.

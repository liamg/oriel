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

- `|` sits before the character under the cursor (in insert mode, before the character after it). `[`…`]` enclose a
  charwise visual selection, and `^` sits before the visual start when it matters. `\` escapes `|[]^\`.
- Values are trimmed. A field with nothing after the colon takes the following lines indented by two spaces as a
  multi-line value (that's how a buffer gets several lines or leading whitespace). Blank lines at the end of such a value
  aren't part of it, but a line holding just the two-space indent is an empty line, even at the end.
- Optional checks: `mode` (`normal`, `insert`, `replace`, `visual`, `visual-line`, `visual-block`, `select`,
  `op-pending`, `cmdline`), `regs`, `marks` (`a=1:0 b=(unset)`), `jumps` (`1:0 3:4`), `undo` (the buffer after the keys,
  `<Esc>` and `u`), `dot` (after the keys, `<Esc>` and `.`), `msg` (the last line of `:messages`; the engine's last notice is worded as nvim words it by
  `vim/vimtest/nvimtext`, since Oriel shows its own text), `win` (`2/3`:
  the current window's number and the tab page's window count, as `winnr()` and `winnr('$')`), `bufs` (`1# 2% 3`: the
  listed buffers in order, `%` on the current one and `#` on the alternate, as `:ls` flags them). The buffer, cursor and
  mode are always the current window's. Positions are `line:col`: 1-based line, 0-based byte column, as `:marks` shows
  them.
- `ref` is `file.txt *tag*` (in `spec/vim`), `nvim/file.txt *tag*` (in `spec/nvim`), `testdir/test_x.vim#Test_name`
  (a function in vim's vendored tests, for mined cases), or `oracle nvim v0.11.7`. A `quote`,
  when given, must sit in that tag's section, whitespace-normalised. `RequireRef` checks both; so does `refcheck`.
- The **runner** (`vim/vimtest`) drives a `vimtest.Engine` (load a buffer, feed keys, snapshot what the case declares)
  and compares every declared field. `undo` and `dot` each replay the case on a fresh engine.
- **Oracle mode** (`go test ./vim/... -oracle`, or `ORIEL_ORACLE=1` for test binaries that don't link `vimtest`): the same
  case runs in `nvim --headless --clean --embed` (`vim/vimtest/nvim`, our own msgpack-RPC client). nvim's default
  mappings are cleared first, and keys go in with `nvim_input`. Buffer, cursor, selection, mode, registers, marks,
  jumps, messages, windows and the buffer list are read back and compared with the case.
  - If **our expectation disagrees with nvim**, the case fails. The test was wrong, not just the code.
  - Where vim and nvim differ, we follow nvim by default (`editor.md` → Options and defaults). A case that follows vim instead says so (`follows: vim`), with the `:help` reference
    that shows the difference. A `follows: vim` or `deviation:` case logs its disagreements with nvim instead of failing.
  - Keys that leave nvim waiting for more (a lone count or register name) can't be read back, so such a case fails.
  - nvim is found at `$ORIEL_NVIM`, then on `PATH`, and must be the pinned version (`nvim.Version`, v0.11.7).
- CI runs both modes. Oracle mode needs nvim, which is pinned in the CI image.
- Until the engine exists (ED-2), engine mode skips and the oracle checks the cases themselves.

### Workflow for an agent adding a feature

1. Find the `:help` tags for the feature. Write cases with `ref` and `quote`, edges included (counts, empty lines, end of file, multibyte, wrap).
2. `refcheck` passes, so the sources are real.
3. `-oracle` passes, so the expectations match nvim.
4. `go test` fails, so the feature is missing (red).
5. Implement until it's green. Each commit stays green.
6. The compatibility table (`editor.md`) regenerates from which tags have passing cases.

### Beyond hand-written cases

- **Mined cases**: vim's own `src/testdir/test_*.vim` (normal, textobjects, visual, change, undo, put, registers,
  increment…)
  are translated into `.vt` cases by a converter. Each case keeps a `ref` to the original test function.
  - The converter (`vim/vimtest/mine`, run with `cd vim && go run ./vimtest/mine/cmd`) replays each test function in
    nvim, statement by statement. Keys typed with `:normal`, `:exe "normal …"` or `feedkeys(…, 'x')` become a case's
    keys; the buffer and cursor before them its `given`; the `assert_equal`s that follow (buffer lines, cursor, registers,
    marks) its `expect`. Ex statements the engine runs (`:s`, `:g`, `:d`…) are typed as `:` command lines, and the
    options it knows (`sw`, `et`, `ic`, `scs`, `ws`, `report`) as a leading `:set`. Anything it can't convert faithfully
    is skipped: other options, mappings, select mode, `gn`, other ex commands, folds, windows, screen-relative motions.
    Fold, window, buffer, quickfix and mapping tests assert through functions (`foldclosed()`, `winnr()`,
    `getqflist()`) the converter can't read back, so those files yield no cases and are covered by hand-written ones.
  - A value vim didn't assert (often the cursor) is taken from nvim, and the case says so in a `# … from oracle` comment.
  - Every case is replayed alone in nvim before it's written; one nvim disagrees with is dropped.
  - The output is `vim/testdata/mined/*.vt`, generated, never edited by hand. `TestMined` runs it against the engine and
    every case must pass, as the hand-written ones in `TestConformance` must. A mined case we choose not to match needs
    a reviewed `deviation:` like any other; since the files are generated, the miner will need a list of them to
    write that from, added with the first one.
- **Differential fuzzing**: random keystroke sequences (weighted towards real usage) run against both
  engines. Any divergence is minimised (delta debugging) into a small `.vt` case and saved as a regression.
  It runs nightly and for a time-boxed slice on each PR.
- **Invariants (property tests)**: `u` after any sequence restores the exact buffer; anchors stay in range;
  the rope matches a naive `[]byte` model; there are no invalid UTF-8 splits; dot-repeat equals replaying the change.

## Everything else

| Module | Tests | Source of truth |
|---|---|---|
| `text` | property tests and fuzzing against a naive model (`[]byte`, per-byte authors, plain-offset anchors); UTF-16 offsets | the model; LSP "position encoding" |
| `syntax` | highlight snapshots per language (query captures over fixture files); unit/text-object ranges | tree-sitter query docs; grammar test corpora |
| `lsp` | recorded JSON-RPC transcripts replayed against a fake server; each message shape cites a spec section | LSP 3.17 |
| `dap` | recorded sessions with real dlv and debugpy, replayed; live runs in CI | DAP spec |
| `term` | `vttest`/esctest-style scripts: bytes in, expected grid out; every case cites a `ctlseqs` entry | xterm ctlseqs, ECMA-48, kitty docs |
| `render`, `ui` | the headless backend records display lists, compared as snapshots (`render/snapshot`); `render/raster` draws them on the CPU (sRGB blending, as CSS) into golden PNGs compared by deltaE OK; the fixture is also held against the mockups (FW-8) | the mockups (`ui.md`) as reference |
| `theme` | token resolution tables; every built-in theme rendered on the fixture workspace; contrast lint | CSS Color 4, APCA |
| `agent/*` | the provider conformance suite (start, send, tool call, permission, interrupt, fork, resume) against a **fake `claude`/`codex` binary** that replays recorded hook and transcript traffic; plus a slow live suite run on demand | recorded sessions from real CLIs, re-recorded when versions bump |
| `wire`, `worker` | in-process worker tests; chaos tests (drop the connection mid-stream, reconnect, compare state) | – |
| `gitx` | fixture repos built by script; porcelain output parsing | git man pages |
| perf | benchmarks for key-to-photon latency, scroll frame time and parse throughput; CI fails on a >10% regression | budgets in `rendering.md` |

## Example: a test that can't hallucinate

```go
func TestConformance(t *testing.T) {
    for _, c := range vimtest.Load(t, "testdata/*/*.vt") {
        t.Run(c.Name, func(t *testing.T) {
            c.RequireRef(t)                          // the tag resolves and the quote is in its section
            c.Compare(t, vimtest.Run(t, engine.New, c)) // buffer, cursor, mode, regs… whatever the case declares
            if vimtest.Oracle() {
                c.Compare(t, vimtest.Run(t, nvim.New(), c)) // the expectation itself must match nvim
            }
        })
    }
}
```

## For agents (also in the repo's CLAUDE.md / AGENTS.md)

- Don't write an expected value from memory. Find it in `spec/`, quote it, or get it from the oracle.
- If the docs are ambiguous, run the oracle and cite the case as `ref: oracle` with the nvim version. Never guess.
- Never weaken or delete a failing test to get to green. Fix the code, or raise a `deviation:` for review.

# Dependencies

Third-party code is a liability we choose deliberately, one dependency at a time.

## Policy

| Tier | What | Rule |
|---|---|---|
| **A · free** | Go stdlib, `golang.org/x/*` (the Go team), and Liam's own `github.com/liamg/*` | use freely; a `liamg/*` module still brings its own dependencies, which are checked as usual |
| **B · large org** | maintained by a large, accountable company: HashiCorp, Google, GitHub/Microsoft, Tailscale, Cloudflare… | allowed; recorded here with the reason |
| **C · ask** | everything else: individual maintainers, small orgs, foundations, startups | **ask Liam first**, every time, before it goes into `go.mod` |
| **Tools** | external binaries we *run*, not link (gopls, delve, rg, claude, codex) | allowed; they run in the sandbox under the user's control |

- **Agents never add a tier-C dependency on their own.** A plan or PR that needs one stops and asks, with:
  what it does, who maintains it, the size of its own dependency tree, and the cost of writing it ourselves.
- **Writing it ourselves is the default alternative.** With agents doing the typing, a 2k-line
  library we fully own often beats a 20k-line one we don't.
- **Transitive dependencies count.** Approving X approves X's tree; this doc lists the tree.

## Enforcement

- `go.mod` is guarded in CI by an **allowlist** (`deps/allow.txt`, module path plus the approved major version).
  Any new module, direct or indirect, fails the build until it's added here and approved (`make depcheck`).
  It checks what we build: every module in a `go.mod` and in `vendor/modules.txt`. Modules that only appear in
  `go mod graph` and are never linked aren't checked.
- The workspace vendor directory (`go work vendor`) is committed. Every upgrade is a reviewed diff of the actual source,
  not just a version bump. CI fails if `vendor/` is out of date (`make vendor-check`).
- `govulncheck` and `go mod verify` run on every PR (`make vulncheck`, `make vendor-check`). Versions are pinned,
  never `latest`, and so are the CI actions (by commit).
- WASM blobs (grammars, the Ghostty VT core) are **built in CI from pinned upstream commits**, with checksums
  committed. We never download prebuilt `.wasm` from anywhere.
- The `oriel build --with` native-plugin path shows a plugin's full module tree and asks before building it in.

## What the plan currently relies on

Each tier-C row is a **question for you**: approve, reject, or "write our own".

| Dependency | Used for | Maintainer | Tier | Alternative if rejected |
|---|---|---|---|---|
| `golang.org/x/{sys,net,crypto,sync,text}` | syscalls, Landlock, netlink, crypto | Go team | A ✓ | – |
| `github.com/hashicorp/yamux` | app ↔ worker multiplexing (from crit) | HashiCorp | B ✓ | – |
| `github.com/infracost/agentbus` | agentbus (from crit) | Infracost | B ✓ (your company) | – |
| ↳ `tailscale.com` (indirect via agentbus) | tsnet | Tailscale | B ✓ | – |
| ↳ `modernc.org/sqlite` + `libc`, `mathutil`, `memory` (indirect via agentbus) | pure-Go SQLite | individual (cznic) | **kept out of Oriel**: agentbus runs as a separate `oriel-agentbus` helper process | the helper has its own module tree; Oriel's has no sqlite |
| `gioui.org` | windowing, GPU, input, IME (**the shell decision**) | Elias Naur + contributors, sponsored | **C ✓ approved**, on condition the spike reproduces the mockups' look with no blocking limits | own windowing layer, or Electron, if the spike fails |
| `github.com/go-text/typesetting` | shaping (HarfBuzz port), font parsing | go-text org (Gio/Fyne maintainers) | **C ✓ approved** | – |
| `github.com/tetratelabs/wazero` | WASM runtime: tree-sitter grammars, Ghostty VT, wasm plugins | Tetrate (company) → community | **C ✓ approved** | – |
| tree-sitter core + per-language grammars (as wasm) | syntax highlighting, text objects | tree-sitter org; **grammars by many individuals** | **C ✓ approved**: grammars pinned per language and added one at a time, run sandboxed in wazero | – |
| nvim-treesitter highlight queries (`.scm` data) | highlight rules | neovim community | **C ✓ approved** (data) | – |
| Ghostty VT core (`libghostty-vt`, as wasm) | terminal emulation | Ghostty, Mitchell Hashimoto (HashiCorp co-founder), non-profit | **C ✓ approved** | – |
| `github.com/yuin/gopher-lua` | Lua VM | individual | **C ✓ approved**, behind `script.VM` | our own Lua VM later, if ever needed |
| `github.com/creack/pty` | PTYs (from crit) | individual; used by Docker, Kubernetes, many others | **rejected: write our own** | `oriel/pty` over `x/sys/unix`, ~300 lines |
| `github.com/alecthomas/chroma` | highlighting (crit today) | individual | C, **dropped**: tree-sitter replaces it | – |
| Fonts: JetBrains Mono, Inter, Space Grotesk | bundled UI fonts (OFL, assets not code) | JetBrains (B); individuals | **✓ approved** (data) | – |

**Not dependencies**: LSP servers, DAP adapters (delve, debugpy…), ripgrep, git, `claude`, `codex`. These are
external programs we spawn, usually inside the sandbox. They're listed in the pre-flight panel, not in `go.mod`.

## The ones that shape the architecture

These four need an answer before phase 0, because they decide the build:

1. ~~**Gio**~~: approved, on condition the spike shows it can match the mockups.
2. ~~**go-text/typesetting**~~: approved.
3. ~~**wazero**~~: approved.
4. ~~**gopher-lua**~~: approved.

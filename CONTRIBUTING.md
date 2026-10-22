# Contributing to Oriel

Thanks for helping. Bug reports are the most useful thing right now: see [Reporting bugs](README.md#reporting-bugs).
For anything bigger than a small fix, open an issue first so we can agree on the approach before you write it.

## Building

You need Go (the version in `go.mod`) and, on Linux, a C compiler and the development headers for Gio's window:

```sh
sudo apt install pkg-config libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-dev libxkbcommon-x11-dev \
  libgles2-mesa-dev libegl1-mesa-dev libffi-dev libxcursor-dev libxfixes-dev libvulkan-dev
```

The quickest build runs the terminal emulator as WebAssembly, with no native library:

```sh
go install github.com/liamg/oriel/cmd/oriel@latest   # or @<tag>
```

The release build links libghostty-vt natively. It needs [zig 0.16.0](https://ziglang.org/download/) (`zig` on
`PATH`, or `ZIG=/path/to/zig`):

```sh
make native   # once: builds libghostty-vt for your platform
make build    # bin/oriel
make run ARGS=~/src/project
make app      # macOS: bin/Oriel.app
make dmg      # macOS: the DMG
```

## Testing

```sh
make test     # every test; the timing budgets run last, one package at a time
make check    # gofmt, vet, dependency, spec-reference, leak, vendor and THIRD_PARTY_NOTICES checks
make oracle   # vim behaviour checked against headless nvim (ORIEL_NVIM=/path/to/nvim)
make test-vt  # the terminal suite on the native backend too (after make native)
```

CI runs all of these on macOS and Linux.

## How the code is written

[docs/architecture.md](docs/architecture.md) describes how the code is laid out, and [AGENTS.md](AGENTS.md) holds the
rules every change follows, whether a person or an agent writes it. In short:

- Test first. Behaviour tests quote a vendored spec under `spec/`, and `make check` verifies every quote.
- Features are built on the same public API a plugin uses. If the API can't express something, extend it.
- Reuse what exists: widgets from `ui/widget`, git from `gitx`, the shared test harnesses.
- Dependencies: the standard library, `golang.org/x/*` and `github.com/liamg/*` only, unless one is approved in
  `tools/depcheck/allowed.txt` first. Ask before adding one.
- Keep comments short, and about the code as it is.
- When you change `vendor/` or anything embedded from elsewhere, run `make notices` and commit `THIRD_PARTY_NOTICES`.

## Plugins and the licence

- A native (Go) plugin built into Oriel (`oriel build --with`, `plugin.Register`) becomes part of the program, so the
  result is under the AGPL-3.0-or-later.
- A Lua or WASM plugin (and any process it starts) that talks to Oriel only through the documented plugin API
  ([docs/api.md](docs/api.md)) isn't required to be AGPL: license it as you like.

## Contributor licence agreement

Oriel is released under the AGPL-3.0-or-later, with the plugin API reference, examples and Lua stubs under
Apache-2.0. To keep the option of also offering Oriel under other terms later (a commercial licence, for example),
contributions are accepted under this agreement. By opening a pull request you agree to it for everything in it.

1. **You keep your copyright.** You aren't assigning it to anyone.
2. **You grant a licence.** You grant Liam Galvin, the project's maintainer, and anyone he passes these rights to, a
   perpetual, worldwide, non-exclusive, royalty-free, irrevocable licence to use, copy, modify, publish, distribute
   and sublicense your contribution, and to release it under any licence, open source or not, including terms other
   than the AGPL.
3. **Patents.** You grant the same people a perpetual, worldwide, non-exclusive, royalty-free, irrevocable licence
   under any patents you hold that your contribution would otherwise infringe, to make, use, sell and distribute it as
   part of Oriel or software built from it.
4. **It's yours to give.** The contribution is your own work, or you have the right to submit it under these terms
   (if your employer has rights to what you write, they've agreed). Anything you include from someone else is marked
   as such, with its licence.
5. **Oriel stays open.** Whatever else happens, your contribution remains available under the licence of the part of
   Oriel it went into (AGPL-3.0-or-later, or Apache-2.0 for the plugin API parts).
6. **No warranty.** You give your contribution as is, and don't have to support it.

Sign off each commit (`git commit -s`) to confirm you agree.

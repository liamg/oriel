<img src="app/testdata/ide-thread.png" alt="Oriel: a session's code and its thread side by side">

# Oriel

Oriel is a native, vim-first IDE built around AI sessions, written in Go. Instead of opening folders you start
**sessions** (work, plan, review, job) on your repositories. Agents work in worktrees and sandboxes while you read,
review and steer: threads on any line, explain-anything, a review ledger, and every running session visible at a glance.

**Status: early, pre-1.0.** Expect rough edges, missing pieces and the odd crash. Bug reports are very welcome (see
[Reporting bugs](#reporting-bugs)).

## Install on macOS

Oriel runs on macOS 12 or later.

1. From the [latest release](https://github.com/liamg/oriel/releases), download the DMG for your Mac:
   - `Oriel-<version>-arm64.dmg` for Apple silicon (M1 and later)
   - `Oriel-<version>-amd64.dmg` for Intel
2. Open it and drag Oriel to Applications.
3. Run this once, before the first launch:

   ```sh
   xattr -dr com.apple.quarantine /Applications/Oriel.app
   ```

   Oriel is signed with its own certificate, not one from Apple, so macOS blocks the downloaded copy ("Oriel can't be
   opened") until you clear the quarantine flag your browser put on it. Updates Oriel downloads itself aren't
   flagged, so you only do this once.

## Install on Linux

Oriel needs glibc 2.35 or later (Ubuntu 22.04, Debian 12, Fedora 36 and newer), on amd64 or arm64.

1. Install the libraries it uses for its window:

   ```sh
   # Debian, Ubuntu
   sudo apt install libx11-6 libx11-xcb1 libxkbcommon0 libxkbcommon-x11-0 libxcursor1 libxfixes3 \
     libegl1 libgles2 libwayland-client0 libwayland-cursor0 libwayland-egl1 libvulkan1

   # Fedora
   sudo dnf install libX11 libX11-xcb libxkbcommon libxkbcommon-x11 libXcursor libXfixes \
     libglvnd-egl libglvnd-gles libwayland-client libwayland-cursor libwayland-egl vulkan-loader
   ```

2. From the [latest release](https://github.com/liamg/oriel/releases), download
   `oriel-<version>-linux-amd64.tar.gz` (or `-arm64`) and put the binary in `~/.local/bin`:

   ```sh
   mkdir -p ~/.local/bin
   tar -xzf oriel-<version>-linux-amd64.tar.gz -C ~/.local/bin --strip-components=1 oriel-<version>-linux-amd64/oriel
   ```

   Oriel updates itself by replacing that file, so keep it somewhere you can write to (not `/usr/bin`).
   `~/.local/bin` must be on your `PATH`.

3. Optionally, add it to your desktop's app launcher with the `.desktop` file and icon from the same archive:

   ```sh
   tar -xzf oriel-<version>-linux-amd64.tar.gz
   cd oriel-<version>-linux-amd64
   sed "s|^Exec=oriel|Exec=$HOME/.local/bin/oriel|" oriel.desktop > ~/.local/share/applications/oriel.desktop
   install -Dm644 oriel.png ~/.local/share/icons/hicolor/256x256/apps/oriel.png
   ```

## Prerequisites

- `git`
- The [`claude` CLI](https://docs.anthropic.com/en/docs/claude-code), installed and logged in. Oriel runs Claude
  through it, on your own subscription.
- Optional:
  - `gh`, logged in with `gh auth login`, to open and review pull requests
  - Docker or Podman, for sandboxed sessions
  - `rg` ([ripgrep](https://github.com/BurntSushi/ripgrep)), for faster search

## First run

Open Oriel from the Dock or your app launcher, or from a terminal on a project:

```sh
oriel ~/src/project
```

Where Oriel keeps things:

|          | macOS                                 | Linux                                              |
|----------|---------------------------------------|----------------------------------------------------|
| Config   | `~/.config/oriel` (`settings.toml`, `init.lua`) | `$XDG_CONFIG_HOME/oriel`, or `~/.config/oriel` |
| State    | `~/Library/Application Support/Oriel` | `$XDG_STATE_HOME/oriel`, or `~/.local/state/oriel` |
| Log      | `~/Library/Logs/Oriel/oriel.log`      | `$XDG_STATE_HOME/oriel/oriel.log`, or `~/.local/state/oriel/oriel.log` |
| Data     | `~/.local/share/oriel`                | `$XDG_DATA_HOME/oriel`, or `~/.local/share/oriel`  |

## Updates

Oriel updates itself: it checks for a new release when it starts and now and then while it runs, installs it, and
runs it from the next restart. Releases are signed, and a new version that fails to start is rolled back.

- `oriel update --check` says whether there's a newer release; `oriel update` installs it.
- To stop automatic updates, set `updates.mode` to `manual` in Settings, or in `~/.config/oriel/settings.toml`:

  ```toml
  [updates]
  mode = "manual"   # or "ask", to be asked before each one
  ```

  With `manual`, Oriel only checks when you run `:UpdateCheck`.

## Known limitations

- On macOS Oriel is self-signed, not notarised by Apple, hence the one-time `xattr` step.
- On Linux there's no package (deb, rpm, Flatpak): just the archive above.
- Claude is the main agent. Codex support is partial.
- No Windows build.

## Reporting bugs

[Open an issue](https://github.com/liamg/oriel/issues/new?template=bug.md) with:

- what you did, what you expected, and what happened instead
- the output of **Copy report**: click the Oriel logo for the About card, or open the log (`:Log`), and press Copy
  report. It holds the version, your OS, the settings you've changed, your plugins, and recent log records and
  events by kind, ID and time. It's redacted (no file contents, transcripts, commands or paths, and your home folder
  shows as `~`), but read it before you paste it.

Security problems: please don't open an issue; see [SECURITY.md](SECURITY.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for building from source, running the tests and how contributions are licensed.

## Licence

Copyright Liam Galvin.

Oriel is free software under the [GNU Affero General Public License v3.0 or later](LICENSE).

The parts you'd use to write a plugin are under the [Apache License 2.0](docs/examples/LICENSE), so a plugin can be under any
licence you like:

- the plugin API reference, [`docs/api.md`](docs/api.md), and the example plugins in [`docs/examples/`](docs/examples)
- [`script/types/`](script/types): the Lua API stubs (`oriel.d.lua`) for your editor's language server

Oriel includes third-party software under their own licences; see [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES).

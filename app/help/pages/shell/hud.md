---
id: shell/hud
covers: [app.overlay.hud, hud.*]
related: [agent/usage, shell/tabs, errors, plugins/lua]
---

# The HUD

The control room as an instrument cluster, over whatever you're doing: plan usage, this machine, sessions by status,
and today's figures. Nothing here is a separate screen.

## Holding it down {#hold}

Hold `⌥` on its own and the HUD comes down from the title bar; let go and it goes. A chord (`⌥O`) doesn't bring it:
the key has to be held alone for a moment (`hud.hold_delay`). Click anywhere on it, or its tab, to pin it: then it
stays until `Esc`, a click on its tab, or the title bar's **⌥ HUD**. `hud.hold` picks another key, or turns holding
off; `hud.toggle` pins it from the keyboard.

{{keys "hud.*"}}
{{options "hud"}}

## Panels {#panels}

Each panel is drawn from what's happening now: a session changing status, the machine's new reading, a log line, a
plan read. Nothing is polled to draw them.

## Plan usage {#usage}

Each account's 5-hour and weekly windows: how much is used, hatched on to where the burn takes it by the reset, and
when they reset. The burn is the slope over the last 30 minutes for the 5-hour window. See Usage for the full page.

## This machine {#machine}

The machine Oriel runs on: its sessions, CPU and memory now, and its CPU over the last 15 minutes. **Performance ↗**
opens the [Performance](shell/performance) page: what Oriel and the processes it started cost.

## Sessions {#sessions}

How many sessions are open and how many need you, the split by status, and the most urgent with why: what it asks,
what it's running, when it replied. Click one to bring it to the front.

## Today {#today}

Since midnight: tokens, cost, the share served from the cache, and requests opened. Plugins add lines under it in the `hud.today` slot.

## Indicators {#indicators}

Indicators are small live readouts in fixed slots: the title bar's right (`title-bar.right`), the status line's sides
(`status-line.left`, `status-line.right`), the HUD's panels (`hud`) and the Today panel (`hud.today`). A plugin that
wants the machine's CPU always in view can put it in one (`MachineChanged` carries it).

## From Lua {#lua}

```lua
oriel.hud.indicator("deploys", function()
  return oriel.ui.indicator{ label = "DEPLOY", value = 4, tone = "warn" }
end, { slot = "title-bar.right", every = "30s" })
oriel.hud.set("deploys", oriel.ui.indicator{ label = "DEPLOY", value = 0 })   -- show this now
oriel.hud.refresh("deploys")                                                  -- or build it again now
```

An indicator is built again every `every`, on each event named in `on` (`on = { "MachineChanged" }`), and when it's
set or refreshed; in between, what it built is kept. `title`, `note` and `span` make it a HUD panel (`slot = "hud"`).

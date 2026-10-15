-- The next meeting under the HUD's Today panel (ui.md 10): from an iCal address, read by this plugin's process
-- (bin/oriel-ical, built from integ/ical/cmd/oriel-ical), or from this Mac's calendars through EventKit.
local L = oriel

local NAME = "ical.next"
local AHEAD = 14 * 24 * 3600 -- how far ahead it looks, in seconds
local STALE = 5 * 60         -- how old the meetings may get before they're read again

L.option{ name = "plugin.ical.source", type = "enum", values = { "ical", "mac" }, default = "ical",
  help = "Where meetings come from: the iCal address below, or this Mac's calendars (every account in Internet Accounts)" }
L.option{ name = "plugin.ical.url", type = "string", default = "",
  help = "Your calendar's secret iCal address (https:// or webcal://). Anybody with it can read the calendar." }
L.option{ name = "plugin.ical.me", type = "list", default = {},
  help = "Your addresses, so the invitations you declined are left out; a Google calendar names its owner itself" }

local state = { events = nil, err = nil, at = nil, busy = false, key = nil }
local proc

local function process()
  if proc == nil or not proc:running() then
    proc = L.process.spawn("bin/oriel-ical")
  end
  return proc
end

-- source is what the meetings come from, or nil when there's nothing to read.
local function source()
  local src, url = L.option["plugin.ical.source"], L.option["plugin.ical.url"] or ""
  if src == "mac" then
    return L.platform == "darwin" and "mac" or nil
  end
  return url ~= "" and ("ical " .. url) or nil
end

local function done(res, err)
  state.busy, state.at = false, L.now()
  if err then
    state.err = tostring(err):gsub("^jsonrpc %-?%d+: ", "")
  else
    state.err, state.events = nil, res.events or {}
  end
  L.hud.refresh(NAME)
end

local function fetch()
  local key = source()
  if key ~= state.key then
    state.events, state.err, state.key = nil, nil, key
  end
  if key == nil or state.busy then
    return
  end
  state.busy = true
  if key == "mac" then
    local script = L.read("eventkit.js")
    -- A minute: the first read waits on macOS's permission prompt.
    L.exec({ "osascript", "-l", "JavaScript", "-e", script, tostring(AHEAD) }, { timeout = 60, on_done = function(out, err)
      if err then
        return done(nil, "could not read this Mac's calendar")
      end
      process():request("eventkit", { output = out, now = L.now() }, done)
    end })
    return
  end
  local req = { url = L.option["plugin.ical.url"], now = L.now() }
  local me = L.option["plugin.ical.me"] or {}
  if #me > 0 then
    req.me = me
  end
  process():request("upcoming", req, done)
end

-- until_text is the time to a start as a dial shows it: 18m, 2h05, 3d.
local function until_text(secs)
  local m = math.ceil(secs / 60)
  if m < 60 then
    return m .. "m"
  end
  local h = math.floor(m / 60)
  if h < 24 then
    return string.format("%dh%02d", h, m % 60)
  end
  return math.floor(h / 24) .. "d"
end

-- place is where the meeting is: a link's host, or the location as written; and how many are invited.
local function place(e)
  local where = e.location or ""
  local host = where:match("^%a+://([^/:]+)") or (where == "" and (e.link or ""):match("^%a+://([^/:]+)")) or nil
  local parts = {}
  if host then
    parts[#parts + 1] = host
  elseif where ~= "" then
    parts[#parts + 1] = where
  end
  if (e.people or 0) > 1 then
    parts[#parts + 1] = e.people .. " people"
  end
  return table.concat(parts, " · ")
end

local function clock(t, now)
  if os.date("%Y%m%d", math.floor(t)) == os.date("%Y%m%d", math.floor(now)) then
    return os.date("%H:%M", math.floor(t))
  end
  return os.date("%a %H:%M", math.floor(t))
end

local function build()
  local now = L.now()
  if source() ~= state.key or state.at == nil or now - state.at >= STALE then
    fetch()
  end
  if state.key == nil then
    return L.ui.column{ L.ui.text("NEXT MEETING", "faint"),
      L.ui.text("No calendar yet: set plugin.ical.url to its iCal address", "dim") }
  end
  local next
  for _, e in ipairs(state.events or {}) do
    if e.finish > now then
      next = e
      break
    end
  end
  if next == nil then
    local why = state.err or (state.events and "Nothing in the next two weeks" or "Reading the calendar…")
    return L.ui.column{ L.ui.text("NEXT MEETING", "faint"), L.ui.text(why, state.err and "warn" or "dim") }
  end
  local ring, head
  if next.start <= now then
    ring = L.ui.ring{ value = (next.finish - now) / math.max(next.finish - next.start, 1), label = "now", name = "meeting ends",
      color = "warn", id = "ring" }
    head = "NOW · UNTIL " .. clock(next.finish, now)
  else
    local left = next.start - now
    ring = L.ui.ring{ value = math.min(left, 3600) / 3600, label = until_text(left), name = "next meeting in",
      color = left <= 300 and "warn" or "accent", id = "ring" }
    head = "NEXT MEETING · " .. clock(next.start, now)
  end
  local lines = { L.ui.text(head, "faint"), L.ui.text(next.title, "title"), gap = 2 }
  local where = place(next)
  if where ~= "" then
    lines[#lines + 1] = L.ui.text(where, "faint")
  end
  if state.err then
    lines[#lines + 1] = L.ui.text(state.err, "warn")
  end
  return L.ui.row{ ring, L.ui.column(lines), gap = 14 }
end

L.hud.indicator(NAME, { slot = "hud.today", every = "30s" }, build)
fetch()

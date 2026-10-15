-- Linear (crit-reference.md → Linear): your assigned tickets in the omnibox (! narrows to them), Start work from one
-- in the launcher with the ticket as the agent's context, the ticket moved into progress as its session starts and
-- into review as its PR is raised, the PR linked back, and the workspace's moves in the HUD. Linear is reached by
-- this plugin's process (bin/oriel-linear, built from integ/linear/cmd/oriel-linear), through Oriel, which holds the
-- API key: neither the process nor this Lua ever sees it.
local L = oriel

local SECRET = "LINEAR_TOKEN"
local NOKEY = "no Linear API key yet"
local STALE = 10 * 60 -- crit's ticket list refresh
local NOKEY_AGAIN = 2 -- how soon to look again while there's no key, as one is being set
local FEED_EVERY = "5m"

local tickets = { issues = nil, err = nil, at = nil, busy = false }
local feed = { events = nil, err = nil, busy = false }
local proc

local function process()
  if proc == nil or not proc:running() then
    proc = L.process.spawn("bin/oriel-linear")
  end
  return proc
end

local function why(err)
  return (tostring(err):gsub("^jsonrpc %-?%d+: ", ""))
end

local function refresh()
  if tickets.busy then
    return
  end
  tickets.busy = true
  process():request("assigned", {}, function(res, err)
    tickets.busy, tickets.at = false, os.time()
    if err then
      tickets.err = why(err)
    else
      tickets.err, tickets.issues = nil, res.issues or {}
    end
  end)
end

local function ask_key()
  L.cmd("secrets.set", { name = SECRET })
end

local function is_key(key)
  return type(key) == "string" and key:match("^%u[%u%d]*%-%d+$") ~= nil
end

-- tone is a state's colour: violet before it starts, cyan in progress, pink in review.
local function tone(state, kind)
  if kind == "started" then
    return state:lower() == "in review" and "status.reviewer" or "status.working"
  end
  return "status.permission"
end

local function note(t)
  local parts = {}
  if t.pr then
    parts[#parts + 1] = "PR #" .. math.floor(t.pr.number)
  end
  if t.blocked then
    parts[#parts + 1] = "blocked"
  end
  if (t.priority or 0) > 0 and t.priorityLabel then
    parts[#parts + 1] = t.priorityLabel:lower()
  end
  return table.concat(parts, " · ")
end

L.picker.source{ id = "tickets", title = "Tickets", prefix = "!", order = 40,
  items = function()
    local stale = tickets.err == NOKEY and NOKEY_AGAIN or STALE
    if tickets.at == nil or os.time() - tickets.at >= stale then
      refresh()
    end
    if tickets.err == NOKEY then
      return { { id = "key", label = "Set your Linear API key", detail = "to list the tickets assigned to you", icon = "◆",
        icon_style = "status.permission", action = "key" } }
    end
    if tickets.err and not tickets.issues then
      return { { id = "err", label = "Linear: " .. tickets.err, icon = "◆", icon_style = "warn", action = "refresh" } }
    end
    local out = {}
    for _, t in ipairs(tickets.issues or {}) do
      out[#out + 1] = { id = t.key, label = t.key .. " " .. t.title, detail = t.project, note = note(t), icon = "◆",
        icon_style = "status.permission", badge = t.state, badge_style = tone(t.state, t.stateType), ticket = t }
    end
    return out
  end,
  run = function(item)
    if item.action == "key" then
      return ask_key()
    elseif item.action == "refresh" then
      tickets.at = nil
      return refresh()
    end
    L.cmd("session.new", { kind = "plan", ticket = item.ticket.key })
  end,
  preview = function(item)
    local t = item.ticket
    if t == nil then
      return L.ui.text(item.label, "dim")
    end
    local rows = { L.ui.text(t.key .. " · " .. t.state:upper(), "faint"), L.ui.text(t.title), gap = 8 }
    local facts = {}
    for _, f in ipairs({ t.project or "", t.priorityLabel or "", t.pr and ("PR #" .. math.floor(t.pr.number) .. " · " .. (t.pr.repo or "")) or "" }) do
      if f ~= "" then
        facts[#facts + 1] = f
      end
    end
    rows[#rows + 1] = L.ui.text(table.concat(facts, " · "), "dim")
    if t.blocked then
      rows[#rows + 1] = L.ui.text("blocked by " .. table.concat(t.blockedBy or {}, ", "), "warn")
    end
    rows[#rows + 1] = L.ui.text("↵ starts work on it: a plan, the ticket its context", "faint")
    return L.ui.column(rows)
  end }

L.ticket.source{ name = "linear", title = "Linear", order = 10, match = is_key,
  lookup = function(key, done)
    process():request("ticket", { key = key }, function(res, err)
      if err then
        return done(nil, why(err))
      end
      done({ key = res.key, title = res.title, url = res.url, state = res.state, state_tone = tone(res.state, res.stateType),
        summary = res.summary, facts = res.facts, context = res.context })
    end)
  end }

-- In progress as its session starts (crit's Start work).
L.on("SessionStart", function(ev)
  if not is_key(ev.ticket) then
    return
  end
  process():request("start", { key = ev.ticket }, function(res, err)
    if err then
      return L.log("linear: couldn't start " .. ev.ticket .. ": " .. why(err), "warn")
    end
    if res.moved then
      tickets.at = nil
      L.notify(ev.ticket .. " moved to " .. res.state .. " in Linear")
    end
  end)
end, { desc = "Linear: the ticket moves into progress" })

-- The PR linked to its ticket, whoever raised it, and the ticket into review while the PR is open.
L.on("PrSeen", function(ev)
  if not is_key(ev.ticket) then
    return
  end
  process():request("link", { key = ev.ticket, url = ev.url, number = ev.number, title = ev.title }, function(_, err)
    if err then
      return L.log("linear: couldn't link " .. ev.url .. " to " .. ev.ticket .. ": " .. why(err), "warn")
    end
    tickets.at = nil
    if ev.state ~= "open" or ev.merged then
      return
    end
    process():request("review", { key = ev.ticket }, function(res, err2)
      if err2 then
        return L.log("linear: couldn't move " .. ev.ticket .. " to review: " .. why(err2), "warn")
      end
      if res.moved then
        L.notify(ev.ticket .. " moved to " .. res.state .. " in Linear")
      end
    end)
  end)
end, { desc = "Linear: link the PR, and the ticket into review" })

-- The workspace's moves, newest first (crit's company feed, its Linear half).
local function fetch_feed()
  if feed.busy then
    return
  end
  feed.busy = true
  process():request("activity", { limit = 6 }, function(res, err)
    feed.busy = false
    if err then
      feed.err = why(err)
    else
      feed.err, feed.events = nil, res.events or {}
    end
    L.hud.refresh("linear.moves")
  end)
end

L.hud.indicator("linear.moves", { slot = "hud", title = "LINEAR", order = 80, every = FEED_EVERY }, function()
  if not feed.busy then
    fetch_feed()
  end
  if feed.err == NOKEY then
    return L.ui.column{ L.ui.text("Set your Linear API key: :LinearKey", "dim") }
  end
  if feed.events == nil then
    return L.ui.column{ L.ui.text(feed.err or "Asking Linear…", feed.err and "warn" or "dim") }
  end
  if #feed.events == 0 then
    return L.ui.column{ L.ui.text("Nothing moved in the last two days", "dim") }
  end
  local rows = { gap = 4 }
  for _, e in ipairs(feed.events) do
    local what = e.kind == "moved" and (e.from .. " → " .. e.to) or "created"
    rows[#rows + 1] = L.ui.row{ L.ui.text(e.ago, "faint"), L.ui.text(e.key, "accent"), L.ui.text(what), L.ui.text(e.who or "", "dim"), gap = 8 }
  end
  return L.ui.column(rows)
end)

L.command("LinearTickets", function() L.cmd("omnibox.open", { scope = "tickets" }) end, { desc = "Your Linear tickets, in the omnibox" })
L.command("LinearKey", ask_key, { desc = "Set your Linear API key" })
L.command("linear.refresh", function()
  tickets.at = nil
  refresh()
  fetch_feed()
end, { desc = "Ask Linear for your tickets and its moves again now" })

-- agentbus (crit-reference.md → agentbus): other people's agents' questions and review requests as inbox cards (ui.md
-- 12), your agent answering a question you didn't get to before its deadline, your presence at the Inbox panel's foot
-- and in the HUD with the bus's log, auto-approve rules, and asking for a review from a raised PR or the assistant.
-- The bus is held by this plugin's process (bin/oriel-agentbus, built from integ/agentbus/cmd/oriel-agentbus);
-- GitHub goes through Oriel, which holds GITHUB_TOKEN: neither the process nor this Lua ever sees it.
local L = oriel

local P = "plugin.agentbus."
local PRESENCES = { "active", "idle", "dnd" }

local state = { snap = nil, err = nil, shown = {}, auto = {}, online = nil, who_at = nil, ticking = false }
local proc
local configure

local function why(err)
  return (tostring(err):gsub("^jsonrpc %-?%d+: ", ""))
end

local function refresh()
  L.hud.refresh("agentbus.mission")
  L.hud.refresh("agentbus")
  L.hud.refresh("agentbus.presence")
  L.hud.refresh("agentbus.bus")
end

local function process()
  if proc == nil or not proc:running() then
    proc = L.process.spawn("bin/oriel-agentbus", {
      on = { changed = function(s)
        state.snap = s
        state.sync()
      end },
      on_exit = function(err)
        state.err = err and ("the agentbus process stopped: " .. why(err)) or nil
        refresh()
      end,
    })
    state.err = nil
  end
  return proc
end

local function opt(name, default)
  local v = L.option[P .. name]
  if v == nil then
    return default
  end
  return v
end

configure = function()
  process():request("configure", {
    url = opt("url", ""), instance = opt("instance", ""), version = L.api.version,
    asks = opt("asks", true), reviews = opt("reviews", true), repos = #opt("repos", {}) > 0 and opt("repos", {}) or nil,
    presence = opt("presence", "active"), rules = opt("auto_approve", ""),
  }, function(res, err)
    if err then
      state.err = why(err)
    else
      state.err, state.snap = nil, res
      state.sync()
    end
    refresh()
  end)
end

L.option{ name = P .. "url", type = "string", default = "", on_change = configure,
  help = "Where agentbus is (ws://agentbus.your-tailnet.ts.net/v1/ws); empty finds it on your tailnet" }
L.option{ name = P .. "instance", type = "string", default = "", on_change = configure,
  help = "This machine's name on the bus; oriel-<host> if empty" }
L.option{ name = P .. "asks", type = "bool", default = true, on_change = configure,
  help = "Take questions from other people's agents" }
L.option{ name = P .. "reviews", type = "bool", default = true, on_change = configure,
  help = "Take review requests for the repositories below" }
L.option{ name = P .. "repos", type = "list", default = {}, on_change = configure,
  help = "The repositories you take review requests for: owner/name, or a glob such as acme/*" }
L.option{ name = P .. "presence", type = "enum", values = PRESENCES, on_change = configure,
  help = "active, idle, or dnd (do not disturb: the bus holds questions and review requests back)" }
L.option{ name = P .. "auto_approve", type = "string", default = "", on_change = configure,
  help = "Auto-approve rules, as JSON: [{ name, enabled, when }]; every way a rule passes needs a paths or maxLines check" }

local function short(user)
  return (user or "someone"):match("^([^@]+)") or user
end

local function asker(from)
  local name = short(from.user)
  if from.session and from.session.agent and from.session.agent ~= "" then
    return name .. "'s agent"
  end
  return name
end

local function label(from)
  local inst = from.instance or {}
  if inst.label and inst.label ~= "" then
    return inst.label
  end
  return (inst.id or ""):match("/(.+)$") or from.node or ""
end

local function act(method, params, after)
  process():request(method, params, function(res, err)
    if err then
      L.notify("agentbus: " .. why(err), "warn")
    elseif after then
      after(res)
    end
  end)
end

local function prNumber(url)
  return (url or ""):match("/pull/(%d+)") or ""
end

-- answerBody is a field under the question to type an answer in; its options are the card's numbered actions.
local function answerBody(it)
  local draft = L.ui.state("agentbus.answer." .. it.id, { text = "" })
  return L.ui.input{ id = "typed", value = draft.text, placeholder = "or type an answer…", label = "Your answer",
    on_change = function(t) draft.text = t end,
    on_submit = function(t)
      act("answer", { id = it.id, text = t }, function()
        draft.text = ""
        L.inbox.remove(it.id)
      end)
    end }
end

local function questionCard(it, now)
  local c = { id = it.id, type = "question", group = "bus", mine = true, icon = "question", tone = "status.needs_you",
    kicker = "agentbus · question", title = asker(it.from) .. " asks", detail = it.ask.question,
    sub = "agentbus · " .. (label(it.from) ~= "" and label(it.from) or short(it.from.user)), interrupt = true,
    body = function() return answerBody(it) end, actions = {} }
  if label(it.from) ~= "" then
    c.title = c.title .. " · " .. label(it.from)
  end
  if (it.deadline_at or 0) > 0 then
    c.expires = math.max(it.deadline_at - now, 0)
    c.note = "else your agent answers, read-only"
  end
  for i, o in ipairs(it.ask.options or {}) do
    c.actions[#c.actions + 1] = { label = o, key = tostring(i), primary = i == 1, run = function() act("answer", { id = it.id, option = o }) end }
  end
  c.actions[#c.actions + 1] = { label = "Later", key = "l", run = function() act("later", { id = it.id }) end }
  return c
end

local function reviewCard(it)
  local r, info = it.review, it.review.info or {}
  local pr = "#" .. prNumber(r.pr)
  local title = short(it.from.user) .. " → you: " .. pr .. (info.title and (" " .. info.title) or "")
  local facts = { r.repo }
  if info.additions or info.deletions then
    facts[#facts + 1] = string.format("+%d -%d", info.additions or 0, info.deletions or 0)
  end
  if info.files then
    facts[#facts + 1] = info.files .. " files"
  end
  local detail = table.concat(facts, " · ")
  if r.note and r.note ~= "" then
    detail = "“" .. r.note .. "” · " .. detail
  end
  return { id = it.id, type = "review-request", group = "bus", mine = true, icon = "review", tone = "violet",
    kicker = "agentbus · review request", title = title, detail = detail, note = "AI review auto-starts",
    sub = "agentbus · " .. table.concat(facts, " · "), interrupt = true,
    actions = {
      { label = "Ignore", key = "i", run = function() act("ignore", { id = it.id }) end },
      { label = "Claim", key = "c", run = function() act("claim", { id = it.id }) end },
      { label = "Claim + Open", key = "o", primary = true, run = function()
        act("claim", { id = it.id }, function() L.cmd("session.start", { kind = "review", pr = r.pr }) end)
      end },
    } }
end

-- sync makes the inbox match the bus: a card for each question and review request shown, none for those gone.
function state.sync()
  local s, now, live = state.snap or {}, L.now(), {}
  for _, it in ipairs(s.items or {}) do
    if not it.hidden and not it.checking then
      local card = it.kind == "ask" and it.ask and questionCard(it, now) or it.review and reviewCard(it)
      if card then
        live[it.id] = true
        L.inbox.add(card)
      end
    end
  end
  for id in pairs(state.shown) do
    if not live[id] then
      L.inbox.remove(id)
    end
  end
  state.shown = live
  for _, a in ipairs(s.auto or {}) do
    if not state.auto[a.id] then
      state.auto[a.id] = true
      local pr = a.repo .. "#" .. prNumber(a.pr)
      L.inbox.add{ id = "auto." .. a.id, type = "auto-approved", group = "bus", icon = "review", done = true,
        title = "Auto-approved " .. pr, sub = "agentbus · rule " .. a.rule .. " · asked by " .. short(a.from.user) }
      L.notify("Auto-approved " .. pr .. " (rule: " .. a.rule .. ")")
    end
  end
  refresh()
end

-- tick hands the questions past their deadline to the agent, on the window's clock, and reads who's online now and
-- then.
local function tick()
  local s = state.snap
  if proc == nil or s == nil or not s.status.connected then
    return
  end
  local now = L.now()
  if not state.ticking then
    state.ticking = true
    proc:request("due", { now = now }, function(res, err)
      state.ticking = false
      for _, d in ipairs(res and res.items or {}) do
        L.agent.fork({ prompt = d.prompt }, function(answer, ferr, who)
          if ferr or answer == nil or answer == "" then
            L.log("agentbus: no answer for " .. short(d.from) .. "'s question: " .. tostring(ferr or "the agent said nothing"), "warn")
            return
          end
          act("auto_answer", { id = d.id, text = answer }, function()
            L.notify("Your agent answered " .. short(d.from) .. " for you" .. (who ~= "" and (" (" .. who .. ")") or ""))
          end)
        end)
      end
    end)
  end
  if state.who_at == nil or now - state.who_at >= 60 then
    state.who_at = now
    proc:request("who", {}, function(res)
      if res then
        state.online = #(res.online or {})
        L.hud.refresh("agentbus.bus")
      end
    end)
  end
end

local function presence()
  local s = state.snap
  return s and s.status.presence or opt("presence", "active")
end

local function chips()
  local nodes = { L.ui.text("presence", "faint") }
  for _, p in ipairs(PRESENCES) do
    nodes[#nodes + 1] = L.ui.button{ id = "presence." .. p, label = p:upper(), primary = presence() == p, on_click = function()
      L.option[P .. "presence"] = p
    end }
  end
  return nodes
end

local function busName()
  local st = state.snap and state.snap.status or {}
  if state.err then
    return state.err, "warn"
  end
  if st.connected then
    return "bus · " .. ((st.instance or ""):match("/(.+)$") or st.instance or ""), "faint"
  end
  return st.error and ("bus · " .. st.error) or "bus · connecting…", st.error and "warn" or "faint"
end

local EVENTS = {
  received = "came in", answered = "answered", later = "put off", ignored = "ignored", claimed = "claimed",
  reviewed = "reviewed", cancelled = "withdrawn", ["auto-approved"] = "auto-approved", requested = "you asked for a review",
  asked = "you asked", ["request-claimed"] = "claimed your request", ["request-reviewed"] = "reviewed your request",
}

local function logLine(e)
  local what = EVENTS[e.event] or e.event
  local text = e.text or ""
  if e.kind == "review" then
    text = text:gsub("^https://github.com/", "")
  end
  local by = e.event == "answered" and e.detail == "agent" and " (your agent)" or ""
  return os.date("%H:%M", math.floor(e.at_s or 0)) .. "  " .. short(e.from.user) .. " · " .. what .. by .. (text ~= "" and (": " .. text) or "")
end

L.hud.indicator("agentbus.presence", { slot = "inbox.foot", help = "plugins/agentbus#presence" }, function()
  local nodes = chips()
  local name, style = busName()
  nodes[#nodes + 1] = L.ui.spacer()
  nodes[#nodes + 1] = L.ui.text(name, style)
  nodes.gap = 8
  return L.ui.row(nodes)
end)

L.hud.indicator("agentbus.mission", { slot = "mission", order = 90, help = "plugins/agentbus#presence" }, function()
  local name, style = busName()
  local row = chips()
  table.remove(row, 1)
  row.gap = 6
  return L.ui.column{ L.ui.text("AGENTBUS", "faint"), L.ui.row(row), L.ui.text(name, style), gap = 4 }
end)

L.hud.indicator("agentbus.bus", { slot = "titlebar.right", order = 75, every = "5s", help = "plugins/agentbus#presence" }, function()
  tick()
  local st = state.snap and state.snap.status or {}
  local dot = "dim"
  if st.connected then
    dot = ({ active = "ok", idle = "warn", dnd = "error" })[st.presence] or "ok"
  end
  return L.ui.indicator{ id = "bus", label = "BUS", value = state.online and tostring(state.online) or "", dot = dot }
end)

L.hud.indicator("agentbus", { slot = "hud", title = "AGENTBUS", span = 2, every = "30s", help = "plugins/agentbus#log" }, function()
  local s = state.snap or {}
  local asks, reviews = 0, 0
  for _, it in ipairs(s.items or {}) do
    if not it.hidden and not it.checking then
      if it.kind == "ask" then asks = asks + 1 else reviews = reviews + 1 end
    end
  end
  local name, style = busName()
  local rows = { L.ui.row(chips()), L.ui.text(name, style),
    L.ui.text(asks .. " questions · " .. reviews .. " review requests waiting", "dim") }
  if s.rules_error then
    rows[#rows + 1] = L.ui.text(s.rules_error, "warn")
  end
  local log = s.log or {}
  if #log == 0 then
    rows[#rows + 1] = L.ui.text("Nothing on the bus yet", "faint")
  end
  for i = 1, math.min(#log, 6) do
    rows[#rows + 1] = L.ui.text(logLine(log[i]), "faint")
  end
  rows.gap = 4
  return L.ui.column(rows)
end)

L.ship.action{ name = "agentbus", order = 60, help = "plugins/agentbus",
  label = function(pr)
    if pr.state == "open" and not pr.merged and state.snap and state.snap.status.connected then
      return "Ask agentbus"
    end
  end,
  run = function(pr)
    act("request_review", { pr = pr.url }, function() L.notify("Asked on agentbus for a review of " .. pr.repo .. "#" .. math.floor(pr.number)) end)
  end }

local function prLabel(pr)
  return (pr or ""):gsub("^https://github.com/([^/]+/[^/]+)/pull/(%d+).*$", "%1#%2")
end

L.agent.tool{ name = "request_review", ["for"] = { "assistant" }, async = true,
  desc = "Ask someone on agentbus to review a pull request. pr is its URL or owner/name#N; who is a person's agentbus address (their email), or empty for anyone who takes reviews for its repository.",
  params = { pr = "string", who = "string?" },
  confirm = function(p)
    local who = (p.who and p.who ~= "") and p.who or "anyone on agentbus who reviews " .. (prLabel(p.pr):match("^[^#]+") or "it")
    return { title = "Ask for a review", lead = "The assistant wants to ask " .. who .. " to review " .. prLabel(p.pr),
      rows = { { key = "pull request", value = prLabel(p.pr), url = p.pr:match("^https://") and p.pr or nil }, { key = "who", value = who } },
      line = "ask for a review of " .. prLabel(p.pr) }
  end,
  run = function(p, done)
    process():request("request_review", { pr = p.pr, to = p.who or "" }, function(res, err)
      if err then
        return done(nil, why(err))
      end
      local who = (p.who and p.who ~= "") and p.who or "anyone"
      done({ result = { id = res.id, state = "pending" }, line = "asked " .. who .. " to review " .. prLabel(p.pr) })
    end)
  end }

L.agent.tool{ name = "review_requests.agentbus", adds = "review_requests", ["for"] = { "assistant" }, read_only = true, reads = "agentbus",
  desc = "The review requests and questions waiting on you on agentbus.", async = true,
  run = function(_, done)
    local out = {}
    for _, it in ipairs(state.snap and state.snap.items or {}) do
      local row = { kind = it.kind, from = it.from.user, claimed = it.claimed or nil, hidden = it.hidden or nil,
        received = os.date("!%Y-%m-%dT%H:%M:%SZ", math.floor(it.created_at or 0)) }
      if it.review then
        row.pr, row.repo, row.note, row.title = it.review.pr, it.review.repo, it.review.note, it.review.info and it.review.info.title
      elseif it.ask then
        row.question, row.options = it.ask.question, it.ask.options
      end
      out[#out + 1] = row
    end
    done(out)
  end }

configure()

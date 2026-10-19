-- Linear (crit-reference.md → Linear) as a ticket tracker: Oriel lists its tickets, starts work from one, moves it as
-- the work goes (into progress, into review, to done on merge) and links the PR back (oriel.ticket.tracker). Linear is
-- reached by this plugin's process (bin/oriel-linear, built from integ/linear/cmd/oriel-linear), through Oriel, which
-- holds the API key: neither the process nor this Lua ever sees it.
local L = oriel

local proc

-- call asks the process, and hands done its answer or why not.
local function call(method, params, done)
  if proc == nil or not proc:running() then
    proc = L.process.spawn("bin/oriel-linear")
  end
  proc:request(method, params, function(res, err)
    if err then
      return done(nil, (tostring(err):gsub("^jsonrpc %-?%d+: ", "")))
    end
    done(res)
  end)
end

L.ticket.tracker{ name = "linear", title = "Linear", icon = "linear", order = 10, keys = "^[A-Z][A-Z0-9]*-[0-9]+$", secret = "LINEAR_TOKEN",
  assigned = function(done)
    call("assigned", {}, function(res, err) done(res and res.tickets, err) end)
  end,
  lookup = function(key, done) call("ticket", { key = key }, done) end,
  start = function(key, done) call("start", { key = key }, done) end,
  review = function(key, done) call("review", { key = key }, done) end,
  done = function(key, done) call("done", { key = key }, done) end,
  link = function(key, pr, done)
    call("link", { key = key, url = pr.url, number = pr.number, title = pr.title }, done)
  end }

-- Its entry points stay hidden until the key is set.
local function keyed() return L.secret.has("LINEAR_TOKEN") end

L.rail.add{ name = "linear", scope = "global", icon = "linear", title = "Linear tickets", order = 25,
  command = "tickets.open", args = { tracker = "linear" }, shown = keyed }
L.start.add{ name = "linear", icon = "linear", title = "Linear tickets", desc = "Your assigned tickets: pick one to plan",
  order = 25, command = "tickets.open", args = { tracker = "linear" }, shown = keyed }

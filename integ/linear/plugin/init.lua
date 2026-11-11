-- Linear as a ticket tracker: Oriel lists its tickets, starts work from one, moves it as the work goes (into progress,
-- into review, and to done when you say so once its PRs are merged) and links the PR back (oriel.ticket.tracker).
-- Linear is reached by this plugin's process (bin/oriel-linear, built from integ/linear/cmd/oriel-linear), through
-- Oriel, which holds the API key: neither the process nor this Lua ever sees it.
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

-- Tools for every session's agent, over MCP: read a ticket, and change one, comment on it or file a new one. They
-- act as the API key's owner, without asking first. An agent with Linear's own MCP server (on its machine, or through
-- its sandbox's gateway) isn't offered them: it has the same tools there.
local linear_mcp = { "mcp.linear.app" }
L.agent.tool{ name = "linear_issue", covered_by = linear_mcp, read_only = true, reads = "linear", async = true,
  desc = "Read a Linear ticket by its key (ENG-123): its title, state, priority, project, assignee, whole description, "
    .. "comments and attachments, in markdown.",
  params = { key = "string" },
  run = function(p, done)
    call("ticket", { key = p.key }, function(res, err) done(res and res.context, err) end)
  end }

L.agent.tool{ name = "linear_update_issue", covered_by = linear_mcp, async = true,
  desc = "Change a Linear ticket (key: ENG-123); what's left out stays as it is. title; description (markdown, "
    .. "replaces the whole description); state (a state of the ticket's team by name: In Progress, Done…); priority "
    .. "(0 none, 1 urgent, 2 high, 3 medium, 4 low); assignee (a name, display name or email; me; none); project "
    .. "(a project by name, as linear_projects lists them, slug or URL, to move it there; none takes it out of its project).",
  params = { key = "string", title = "string?", description = "string?", state = "string?", priority = "number?",
    assignee = "string?", project = "string?" },
  run = function(p, done) call("update", p, done) end }

L.agent.tool{ name = "linear_comment", covered_by = linear_mcp, async = true,
  desc = "Comment on a Linear ticket (key: ENG-123), in markdown, as the user.",
  params = { key = "string", body = "string" },
  run = function(p, done) call("comment", p, done) end }

L.agent.tool{ name = "linear_create_issue", covered_by = linear_mcp, async = true,
  desc = "File a new Linear ticket. team (its key, ENG, or name; needed when there's more than one); title; "
    .. "description (markdown); state (a state of the team by name); priority (0 none, 1 urgent, 2 high, 3 medium, "
    .. "4 low); assignee (a name, display name or email; me); project (a project by name, as linear_projects lists "
    .. "them, slug or URL). Answers with its key and URL.",
  params = { title = "string", team = "string?", description = "string?", state = "string?", priority = "number?",
    assignee = "string?", project = "string?" },
  run = function(p, done) call("create", p, done) end }

L.agent.tool{ name = "linear_projects", covered_by = linear_mcp, read_only = true, reads = "linear", async = true,
  desc = "List Linear's projects, to file a ticket in one or move one there: each one's name, slug, status, teams "
    .. "and URL. team (its key, ENG, or name) keeps those of one team; query keeps those with it in their names.",
  params = { team = "string?", query = "string?" },
  run = function(p, done)
    call("projects", p, function(res, err) done(res and res.projects, err) end)
  end }

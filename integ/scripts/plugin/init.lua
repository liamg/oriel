-- Scripts of your own, run on the session in front: deploy its projects, open its workspace in tmux. Each slot in the
-- code becomes a reference to an environment variable holding its value, so a value never runs as code.
local L = oriel

-- user_only: the list is code run under this plugin's exec grant, so no other plugin may write it.
L.option{ name = "plugin.scripts.list", type = "map", user_only = true,
  help = "Your scripts, by name: shell code, run with bash (sh without it), or under the interpreter a first #! line "
    .. "names. {session} is the session's workspace, {session_name} its name and {primary} its primary project's "
    .. "checkout (the workspace when it has none). A script naming {repo} or {repo_name} runs once per project, in "
    .. "its checkout. ${x} stays the shell's, {{session}} is a literal {session}, and any other {{word}} stays as "
    .. "written. Each slot reads its environment variable (ORIEL_SCRIPT_SESSION, ORIEL_SCRIPT_REPO_NAME…), which a #! "
    .. "script that isn't a shell reads in place of slots." }

local SLOTS = { session = true, session_name = true, primary = true, repo = true, repo_name = true }
local TIMEOUT = 600 -- seconds: a deploy can take minutes
local TAIL = 5      -- lines of output a notice has room for
local KEEP = 64 * 1024 -- bytes of output kept: the notice shows only its last lines

-- RUNNER runs the code in $1, with each NAME=value after it in its environment. Code with a #! line goes to the
-- interpreter that line names, read from fd 3: no temp file to need an exec mount or outlive a killed run, and the
-- script's stdin stays its own. The plugin may only start sh itself.
local RUNNER = [[
exec 2>&1
code=$1; shift
for kv in "$@"; do export "$kv"; done
case $code in
'#!'*)
  line=${code%%"
"*}
  line=${line%"$(printf '\r')"}
  set -f; set -- ${line#'#!'}; set +f
  printf '%s' "$code" | "$@" /dev/fd/3 3<&0 < /dev/null ;;
*)
  if command -v bash > /dev/null 2>&1; then exec bash -c "$code"; fi
  exec sh -c "$code" ;;
esac]]

-- LITERAL stands in for a {{word}} while slots are found: no script holds a NUL. {{slot}} is the way to write a
-- literal {slot}; any other {{word}} stays as written, as Go templates ({{end}}) need it.
local LITERAL = "\0"

-- unescaped is code with each {{word}} hidden from the slot patterns.
local function unescaped(code)
  return (code:gsub("{{([a-z_]+)}}", LITERAL .. "%1" .. LITERAL))
end

-- each calls fn(pos, whole, name) for every {name} in code that isn't the shell's ${name} or inside a {{word}}.
local function each(code, fn)
  code = unescaped(code)
  for pos, whole, name in code:gmatch("()({([a-z_]+)})") do
    if pos == 1 or code:sub(pos - 1, pos - 1) ~= "$" then
      fn(pos, whole, name)
    end
  end
end

local function per_project(code)
  local yes = false
  each(code, function(_, _, name) yes = yes or name == "repo" or name == "repo_name" end)
  return yes
end

local function unknown(code)
  local bad
  each(code, function(_, whole, name) bad = bad or (not SLOTS[name] and whole) end)
  return bad
end

-- heredoc marks the bodies of the heredocs in pending, from pos, in at: "<", or "<'" for a quoted delimiter's,
-- where nothing expands. It's where the code goes on.
local function heredoc(code, pos, pending, at)
  for _, h in ipairs(pending) do
    while pos <= #code do
      local eol = code:find("\n", pos, true) or #code + 1
      local line = code:sub(pos, eol - 1):gsub("\r$", "")
      if (h.tabs and line:gsub("^\t+", "") or line) == h.word then
        pos = eol + 1
        break
      end
      for j = pos, eol do
        at[j] = h.quoted and "<'" or "<"
      end
      pos = eol + 1
    end
  end
  return pos
end

-- quoting is the shell quoting at each byte of code: nil outside quotes, "'" or '"' inside them, "<" or "<'" in a
-- heredoc's body. A $(…) or `…` starts afresh, unquoted, even within "…". It doesn't follow $'…' or a case's ) inside $(…):
-- a wrong guess only changes how a slot reads, since fill never puts a value in the code.
local function quoting(code)
  local at, outer, pending = {}, {}, {}
  local q, depth, i = nil, 0, 1
  local function open(closer)
    outer[#outer + 1] = { q = q, depth = depth, closer = closer }
    q, depth = nil, 0
  end
  local function close()
    local o = table.remove(outer)
    q, depth = o.q, o.depth
  end
  while i <= #code do
    local c, closer = code:sub(i, i), outer[#outer] and outer[#outer].closer
    at[i] = q
    if c == "\\" and q ~= "'" then
      at[i + 1] = q
      i = i + 1
    elseif c == "<" and not q and code:find("^<<[^<]", i) then
      local _, e, tabs, bs, mark, word = code:find("^<<(%-?)[ \t]*(\\?)(['\"]?)([%a_][%w_]*)%3", i)
      if e then
        pending[#pending + 1] = { word = word, tabs = tabs == "-", quoted = bs .. mark ~= "" }
        i = e
      end
    elseif c == "\n" and not q and #pending > 0 then
      i = heredoc(code, i + 1, pending, at) - 1
      pending = {}
    elseif c == "$" and q ~= "'" and code:sub(i + 1, i + 1) == "(" then
      open(")")
      i = i + 1
    elseif c == "`" and not q and closer == "`" then
      close()
    elseif c == "`" and q ~= "'" then
      open("`")
    elseif c == "(" and not q then
      depth = depth + 1
    elseif c == ")" and not q and depth > 0 then
      depth = depth - 1
    elseif c == ")" and not q and closer == ")" then
      close()
    elseif c == "'" and q ~= '"' then
      q = not q and "'" or nil
    elseif c == '"' and q ~= "'" then
      q = not q and '"' or nil
    elseif c == "#" and not q and (i == 1 or code:sub(i - 1, i - 1):match("[%s;&|()]")) then
      -- A comment's quotes don't count: "# don't" opens nothing.
      i = (code:find("\n", i, true) or #code + 1) - 1
    end
    i = i + 1
  end
  return at
end

local SHELLS = { sh = true, bash = true, zsh = true, dash = true, ksh = true, mksh = true, ash = true, fish = true }

-- shell is the shell that runs code ("fish", or "sh" for the POSIX ones), or nil for an interpreter that isn't one.
local function shell(code)
  local line = code:match("^#!([^\n]*)")
  if not line then
    return "sh"
  end
  local words = {}
  for w in line:gsub("\r$", ""):gmatch("%S+") do
    words[#words + 1] = w
  end
  local i = 1
  if words[1] and words[1]:match("[^/]+$") == "env" then
    i = 2
    while words[i] and (words[i]:sub(1, 1) == "-" or words[i]:find("^[%w_]+=")) do
      i = i + 1
    end
  end
  local name = words[i] and words[i]:match("[^/]+$")
  return SHELLS[name] and (name == "fish" and "fish" or "sh") or nil
end

-- fill is the slot name as a reference to its environment variable, one word where the quoting is q. The value
-- itself never goes in the code, so it can't run as code however the quoting was read. A letter or [ after it
-- would read as part of the name: POSIX shells take ${V}; fish has no ${…}, so its quotes close around $V, which
-- fish never splits.
local function fill(name, q, sh, after)
  local v = "ORIEL_SCRIPT_" .. name:upper()
  if q == "'" then
    return "'\"$" .. v .. "\"'"
  elseif q == "<" then
    return "${" .. v .. "}"
  elseif q == '"' and after:match("[%w_%[]") then
    return sh == "fish" and '"$' .. v .. '"' or "${" .. v .. "}"
  elseif q == '"' then
    return "$" .. v
  end
  return '"$' .. v .. '"'
end

local function expand(code, sh)
  code = unescaped(code)
  local at = quoting(code)
  code = code:gsub("()({([a-z_]+)})()", function(pos, whole, name, after)
    if pos > 1 and code:sub(pos - 1, pos - 1) == "$" then
      return whole
    end
    return fill(name, at[pos], sh, code:sub(after, after))
  end)
  return (code:gsub(LITERAL .. "([a-z_]+)" .. LITERAL, function(word)
    return SLOTS[word] and "{" .. word .. "}" or "{{" .. word .. "}}"
  end))
end

-- tail is the last lines of out, without colors or carriage returns.
local function tail(out)
  out = (out or ""):gsub("\r", ""):gsub("\27%[[%d;?]*%a", "")
  local lines = {}
  for line in out:gmatch("[^\n]+") do
    lines[#lines + 1] = line
  end
  return table.concat(lines, "\n", math.max(1, #lines - TAIL + 1))
end

local function scripts()
  return L.option["plugin.scripts.list"] or {}
end

local function fail(msg)
  L.notify(msg, "error")
end

local function run(name, project)
  local code = scripts()[name]
  if not code then
    return fail("No script named " .. name .. " in plugin.scripts.list")
  end
  local bad = unknown(code)
  if bad then
    return fail(name .. ": " .. bad .. " isn't a slot: use {session}, {session_name}, {primary}, {repo} or "
      .. "{repo_name}, or write " .. bad:gsub("^{(.*)}$", "{ %1 }") .. " with spaces for the text itself")
  end
  local sh, slot = shell(code), nil
  each(code, function(_, whole, n) slot = slot or (SLOTS[n] and { whole, n }) end)
  if not sh and slot then
    return fail(name .. ": " .. slot[1] .. " works only in shell code: read ORIEL_SCRIPT_" .. slot[2]:upper()
      .. " from the environment instead")
  end
  local at, quoted = quoting(unescaped(code)), nil
  each(code, function(pos, whole, n) quoted = quoted or (SLOTS[n] and at[pos] == "<'" and whole) end)
  if quoted then
    return fail(name .. ": " .. quoted .. " can't go in a quoted heredoc (<<'EOF'), where nothing expands: use <<EOF, "
      .. "and write any other $ in it as \\$")
  end
  local se = L.session.current()
  if not se then
    return fail(name .. " runs on a session: open one first")
  end
  local vals, dir = { session = se.dir, session_name = se.name ~= "" and se.name or se.id, primary = se.dir }, se.dir
  for _, p in ipairs(se.projects) do
    if p.primary then
      vals.primary = p.dir
    end
  end
  if per_project(code) then
    if not project or project == "" then
      return fail(name .. " runs on one project: say which project")
    end
    for _, p in ipairs(se.projects) do
      if p.name == project then
        vals.repo, vals.repo_name, dir = p.dir, p.name, p.dir
      end
    end
    if not vals.repo then
      return fail(name .. ": the session has no project " .. project)
    end
  end
  -- Slots read their values from the environment. ORIEL_SESSION is taken: it marks a session's own processes.
  local argv = { "sh", "-c", RUNNER, "oriel-script", expand(code, sh) }
  for k, v in pairs(vals) do
    argv[#argv + 1] = "ORIEL_SCRIPT_" .. k:upper() .. "=" .. v
  end
  L.exec_async(argv, { cwd = dir, timeout = TIMEOUT, tail = KEEP },
    function(out, err)
      local title = vals.repo_name and (name .. " · " .. vals.repo_name) or name
      if err then
        return fail(title .. " failed (" .. err:gsub(":%s*$", "") .. ")\n" .. tail(out))
      end
      L.notify(title .. " finished\n" .. tail(out), "info")
    end)
end

L.command("scripts.run", function(args) run(args.name, args.project) end, {
  desc = "Run one of your scripts (plugin.scripts.list) on the session in front: {name}, and {project} for one that runs per project",
  args = { { name = "name", type = "string", required = true }, { name = "project", type = "string" } } })

L.command("scripts.pick", function() L.run("picker.open", { source = "scripts" }) end, {
  desc = "Pick one of your scripts to run on the session in front", ex = "Scripts" })

L.picker.source{ name = "scripts", title = "Scripts", prefix = "$", order = 60,
  items = function()
    local names = {}
    for name in pairs(scripts()) do
      names[#names + 1] = name
    end
    table.sort(names)
    local se = L.session.current()
    local out = {}
    for _, name in ipairs(names) do
      if not per_project(scripts()[name]) then
        out[#out + 1] = { label = name, detail = "script", script = name }
      elseif se then
        for _, p in ipairs(se.projects) do
          out[#out + 1] = { label = name .. " · " .. p.name, detail = "script", script = name, project = p.name }
        end
      end
    end
    return out
  end,
  run = function(item) run(item.script, item.project) end }

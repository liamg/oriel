-- Scripts of your own, run on the session in front: deploy its projects, open its workspace in tmux. Slots in the
-- code are filled in before it runs, each single-quoted, so a value with spaces or quotes stays one shell word.
local L = oriel

L.option{ name = "plugin.scripts.list", type = "map",
  help = "Your scripts, by name: shell code, run with bash (sh without it), or under the interpreter a first #! line "
    .. "names. {session} is the session's workspace, {session_name} its name and {primary} its primary project's "
    .. "checkout (the workspace when it has none). A script naming {repo} or "
    .. "{repo_name} runs once per project, in its checkout. ${x} stays the shell's." }

local SLOTS = { session = true, session_name = true, primary = true, repo = true, repo_name = true }
local TIMEOUT = 600 -- seconds: a deploy can take minutes
local TAIL = 5      -- lines of output a notice has room for

-- RUNNER runs the code in $1. Code with a #! line goes through a file, so the OS picks its interpreter: the
-- plugin may only start sh itself.
local RUNNER = [[
exec 2>&1
case $1 in
'#!'*)
  f=$(mktemp) || exit 1
  trap 'rm -f "$f"' EXIT
  printf '%s' "$1" > "$f" && chmod +x "$f" && "$f" ;;
*)
  if command -v bash > /dev/null 2>&1; then exec bash -c "$1"; fi
  exec sh -c "$1" ;;
esac]]

local function quote(s)
  return "'" .. (s:gsub("'", [['\'']])) .. "'"
end

-- each calls fn(pos, whole, name) for every {name} in code that isn't the shell's ${name}.
local function each(code, fn)
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

local function expand(code, vals)
  return (code:gsub("()({([a-z_]+)})", function(pos, whole, name)
    if pos > 1 and code:sub(pos - 1, pos - 1) == "$" then
      return whole
    end
    return quote(vals[name])
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
    return fail(name .. ": " .. bad .. " isn't a slot: use {session}, {session_name}, {primary}, {repo} or {repo_name}")
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
  L.exec_async({ "sh", "-c", RUNNER, "oriel-script", expand(code, vals) }, { cwd = dir, timeout = TIMEOUT },
    function(out, err)
      local title = project and (name .. " · " .. project) or name
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

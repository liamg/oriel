-- Scripts of your own, run on the session in front: deploy its projects, open its workspace in tmux. The code runs
-- as written; the session's values reach it only through the environment, so a value never runs as code.
local L = oriel

-- user_only: the list is code run under this plugin's exec grant, so no other plugin may write it.
L.option{ name = "plugin.scripts.list", type = "map", user_only = true,
  help = "Your scripts, by name: shell code, run with bash (sh without it), or under the interpreter a first #! line "
    .. "names. Each runs with ORIEL_SCRIPT_SESSION (the session's workspace), ORIEL_SCRIPT_SESSION_NAME and "
    .. "ORIEL_SCRIPT_PRIMARY (its primary project's checkout, the workspace when it has none) in its environment. A "
    .. "script that mentions ORIEL_SCRIPT_REPO or ORIEL_SCRIPT_REPO_NAME runs once per project, in its checkout, "
    .. "with those set too." }

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

-- per_project: a script that reads the project's checkout or name needs one to run for. Matching the text works for
-- any language.
local function per_project(code)
  return code:find("ORIEL_SCRIPT_REPO", 1, true) ~= nil
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
  local se = L.session.current()
  if not se then
    return fail(name .. " runs on a session: open one first")
  end
  if se.dir == "" then
    return fail(name .. " needs a session with a workspace: " .. (se.name ~= "" and se.name or se.id) .. " has none yet")
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
  -- ORIEL_SESSION is taken: it marks a session's own processes.
  local argv = { "sh", "-c", RUNNER, "oriel-script", code }
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

-- user_only: a script is yours to start, never another plugin's.
L.command("scripts.run", function(args) run(args.name, args.project) end, { user_only = true,
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

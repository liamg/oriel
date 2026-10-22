-- Azure DevOps as a code host: the pull requests asking for your review on the Reviews page, and a
-- project's own raised, read and merged from the Ship page (oriel.code.host), on dev.azure.com and on an Azure DevOps
-- Server you set up. Azure DevOps is reached by this plugin's process (bin/oriel-azure, built from
-- integ/azure/cmd/oriel-azure), through Oriel, which holds each site's token: neither the process nor this Lua ever
-- sees it.
local L = oriel

local proc
-- orgs are each site's organizations learned from the repos and pull requests asked about, by its api, kept across
-- the process.
local orgs = {}

-- call asks the process, and hands done its answer or why not.
local function call(method, params, done)
  if proc == nil or not proc:running() then
    proc = L.process.spawn("bin/oriel-azure")
  end
  proc:request(method, params, function(res, err)
    if err then
      return done(nil, (tostring(err):gsub("^jsonrpc %-?%d+: ", "")))
    end
    done(res)
  end)
end

-- none is done with nothing to give back.
local function none(done)
  return function(_, err) done(nil, err) end
end

local cloud = { api = "https://dev.azure.com", secret = "AZURE_DEVOPS_TOKEN" }

-- Your organizations on dev.azure.com, to list the pull requests waiting on you in; those learned from your repos are
-- added.
local organizations = "plugin.azure-devops.organizations"
L.option{ name = organizations, type = "list",
  help = "Your organizations on dev.azure.com, whose pull requests waiting on your review the Reviews page lists. Those of your repos are found without being named here." }

L.code.host{ name = "azure", title = "Azure DevOps", icon = "azure", order = 30,
  words = { short = "PR", long = "pull request", sigil = "!" },
  -- az devops' own token, when no secret is set; az login's are Azure AD tokens, which Basic doesn't take.
  tokens = { env = { "AZURE_DEVOPS_EXT_PAT" } },
  sites = {
    { host = "dev.azure.com", api = cloud.api, secret = cloud.secret,
      aliases = { "ssh.dev.azure.com", "vs-ssh.visualstudio.com" } },
  },
  -- …/{org}/{project}/_git/{repo} over https (a server's collection in place of the org), v3/{org}/{project}/{repo}
  -- over ssh.
  path = [[^/(?:v3/[^/]+/[^/]+|.*/_git)/([^/]+?)(?:\.git)?/?$]],
  takes = [[/_git/[^/]+/pullrequest/\d+$]],
  shape = [[/_git/]],
  setup = { api = "https://{host}/DefaultCollection", secret = "AZURE_DEVOPS_TOKEN",
    token_url = "https://{host}/_usersSettings/tokens",
    token_help = "A personal access token with Code: read & write, to list and raise pull requests, and Identity: read, to ask reviewers by name.",
    probe = function(host, done)
      call("probe", { host = host }, function(res, err) done(res ~= nil and res.server == true, err) end)
    end },
  requested = function(site, done)
    local known = {}
    for _, o in ipairs(orgs[site.api] or {}) do table.insert(known, o) end
    if site.api == cloud.api then
      for _, o in ipairs(L.option[organizations] or {}) do table.insert(known, o) end
    end
    -- An empty table would go as an object, not a list.
    call("requested", { site = site, orgs = #known > 0 and known or nil }, function(res, err)
      if res and res.orgs and #res.orgs > 0 then
        orgs[site.api] = res.orgs
      end
      done(res and res.requests, err)
    end)
  end,
  lookup = function(site, url, done) call("lookup", { site = site, url = url }, done) end,
  checkout = function(site, url, done) call("checkout", { site = site, url = url }, done) end,
  for_branch = function(repo, branch, done) call("for_branch", { repo = repo, branch = branch }, done) end,
  create = function(repo, pr, done) call("create", { repo = repo, new = pr }, done) end,
  edit = function(repo, pr, title, body, done) call("edit", { repo = repo, pr = pr, title = title, body = body }, done) end,
  ready = function(repo, pr, done) call("ready", { repo = repo, pr = pr }, none(done)) end,
  request_reviewers = function(repo, pr, who, done) call("request_reviewers", { repo = repo, pr = pr, who = who }, none(done)) end,
  checks = function(repo, pr, done) call("checks", { repo = repo, pr = pr }, done) end,
  merge_methods = function(repo, pr, done) call("merge_methods", { repo = repo, pr = pr }, done) end,
  blockers = function(repo, pr, done) call("blockers", { repo = repo, pr = pr }, done) end,
  merge = function(repo, pr, opts, done) call("merge", { repo = repo, pr = pr, merge = opts }, none(done)) end,
  threads = function(repo, pr, done) call("threads", { repo = repo, pr = pr }, done) end,
  reply = function(repo, pr, thread, body, done)
    call("reply", { repo = repo, pr = pr, thread_id = thread.id or "", body = body }, done)
  end }

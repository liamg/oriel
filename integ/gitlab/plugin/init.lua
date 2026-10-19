-- GitLab as a code host (hosts.md), gitlab.com and the self-hosted sites Oriel sets up with you: its merge requests
-- asking for your review list on the Reviews page, and a project whose origin is on one raises, reads and merges its
-- merge request from the Ship page (oriel.code.host). Every call names its site. GitLab is reached by this plugin's process (bin/oriel-gitlab, built from
-- integ/gitlab/cmd/oriel-gitlab), through Oriel, which holds the token: neither the process nor this Lua sees it.
local L = oriel

local proc

-- call asks the process, and hands done its answer, or the field of it named, or why not.
local function call(method, params, done, field)
  if proc == nil or not proc:running() then
    proc = L.process.spawn("bin/oriel-gitlab")
  end
  proc:request(method, params, function(res, err)
    if err then
      return done(nil, (tostring(err):gsub("^jsonrpc %-?%d+: ", "")))
    end
    if field then
      return done(res and res[field])
    end
    done(res)
  end)
end

L.code.host{ name = "gitlab", title = "GitLab", icon = "gitlab", order = 20, secret = "GITLAB_TOKEN",
  words = { short = "MR", long = "merge request", sigil = "!" },
  sites = { { host = "gitlab.com", api = "https://gitlab.com/api/v4", secret = "GITLAB_TOKEN" } },
  tokens = { env = { "GITLAB_TOKEN" }, command = { "glab", "config", "get", "token", "--host", "{host}" } },
  path = [[^/?(.+?)(?:\.git)?/?$]],
  takes = [[/-/merge_requests/\d+]],
  setup = { api = "https://{host}/api/v4", secret = "GITLAB_TOKEN",
    token_url = "https://{host}/-/user_settings/personal_access_tokens?name=Oriel&scopes=api",
    token_help = "A personal access token with the api scope: Oriel reads your merge requests, and raises and merges them when you ask.",
    probe = function(host, done) call("probe", { host = host }, done, "gitlab") end },
  requested = function(site, done) call("requested", { site = site }, done, "requests") end,
  lookup = function(site, url, done) call("lookup", { site = site, url = url }, done) end,
  checkout = function(site, url, done) call("checkout", { site = site, url = url }, done) end,
  for_branch = function(repo, branch, done) call("for_branch", { repo = repo, branch = branch }, done, "pr") end,
  create = function(repo, new, done) call("create", { repo = repo, new = new }, done) end,
  edit = function(repo, pr, title, body, done) call("edit", { repo = repo, pr = pr, title = title, body = body }, done) end,
  ready = function(repo, pr, done) call("ready", { repo = repo, pr = pr }, done) end,
  request_reviewers = function(repo, pr, who, done) call("request_reviewers", { repo = repo, pr = pr, who = who }, done) end,
  checks = function(repo, pr, done) call("checks", { repo = repo, pr = pr }, done, "checks") end,
  merge_methods = function(repo, pr, done) call("merge_methods", { repo = repo, pr = pr }, done) end,
  blockers = function(repo, pr, done) call("blockers", { repo = repo, pr = pr }, done, "blockers") end,
  merge = function(repo, pr, opts, done) call("merge", { repo = repo, pr = pr, opts = opts }, done) end,
  threads = function(repo, pr, done) call("threads", { repo = repo, pr = pr }, done, "threads") end,
  reply = function(repo, pr, thread, body, done)
    call("reply", { repo = repo, pr = pr, thread = thread.id or "", body = body }, done, "id")
  end }

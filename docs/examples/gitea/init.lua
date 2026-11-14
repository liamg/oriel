-- Gitea as a code host (oriel.code.host), in plain Lua: every call is an oriel.http.request to the site's API, with
-- its token named, never read. Oriel finds the site (gitea.com, or a server of yours it set up), the token (yours, else
-- $GITEA_TOKEN, else it asks) and builds the Reviews page and the Ship page's pull request step on these functions.
local L = oriel

-- api calls Gitea's API on site and hands done the decoded answer, or nil and why not.
local function api(site, method, path, body, done)
  L.http.request({ method = method, url = site.api .. path, auth = site.secret, auth_scheme = "token",
    headers = { ["Content-Type"] = "application/json", Accept = "application/json" },
    body = body and L.json_encode(body) or nil }, function(res, err)
    if err then return done(nil, err) end
    if res.status >= 400 then return done(nil, "Gitea: HTTP " .. res.status .. " " .. (res.body or "")) end
    if res.body == nil or res.body == "" then return done({}) end
    done(L.json_decode(res.body) or {})
  end)
end

-- repo_path is a repo's API path: /repos/owner/name.
local function repo_path(repo) return "/repos/" .. repo end

-- wip says a title marks a draft, as Gitea does.
local function wip(title) return title:match("^%s*WIP:") ~= nil or title:match("^%s*%[WIP%]") ~= nil end

-- logins are the users' logins, or nil for none: an empty table would go as an object, not a list.
local function logins(users)
  local out = {}
  for _, u in ipairs(users or {}) do table.insert(out, u.login) end
  return #out > 0 and out or nil
end

-- pr_of is a Gitea pull request as Oriel takes it.
local function pr_of(p)
  return { number = p.number, id = tostring(p.id), title = p.title, body = p.body or "", url = p.html_url,
    author = p.user and p.user.login, state = p.state, draft = wip(p.title), merged = p.merged == true,
    branch = p.head.ref, base = p.base.ref, head = p.head.sha, reviewers = logins(p.requested_reviewers) }
end

-- of_url splits a pull request's URL into its repo and number.
local function of_url(url)
  local repo, n = url:match("^https?://[^/]+/([^/]+/[^/]+)/pulls/(%d+)")
  return repo, tonumber(n)
end

-- check_state is a commit status as a check's state.
local states = { success = "ok", warning = "ok", failure = "failed", error = "failed", pending = "running" }

-- checks are the head's statuses, the newest of each context.
local function checks(site, repo, sha, done)
  api(site, "GET", repo_path(repo) .. "/commits/" .. sha .. "/statuses", nil, function(list, err)
    if err then return done(nil, err) end
    local out, seen = {}, {}
    for _, s in ipairs(list) do
      if not seen[s.context] then
        seen[s.context] = true
        table.insert(out, { name = s.context, state = states[s.status] or "queued", url = s.target_url,
          started = s.created_at, completed = s.status ~= "pending" and s.updated_at or nil })
      end
    end
    done(out)
  end)
end

-- reviews counts what the pull request's reviews leave open: comments no one resolved, and who asked for changes.
local function reviews(site, repo, n, done)
  api(site, "GET", repo_path(repo) .. "/pulls/" .. n .. "/reviews", nil, function(list, err)
    if err then return done(nil, err) end
    local changes, latest, ids = {}, {}, {}
    for _, r in ipairs(list) do
      if not r.dismissed and r.user then latest[r.user.login] = r.state end
      if (r.comments_count or 0) > 0 then table.insert(ids, r.id) end
    end
    for who, state in pairs(latest) do
      if state == "REQUEST_CHANGES" then table.insert(changes, who) end
    end
    local open, left = 0, #ids
    if left == 0 then return done({ unresolved = 0, changes = changes }) end
    for _, id in ipairs(ids) do
      api(site, "GET", repo_path(repo) .. "/pulls/" .. n .. "/reviews/" .. id .. "/comments", nil, function(cs)
        for _, c in ipairs(cs or {}) do
          if c.resolver == nil then open = open + 1 end
        end
        left = left - 1
        if left == 0 then done({ unresolved = open, changes = changes }) end
      end)
    end
  end)
end

-- all runs n calls of step(i, done1) at once and calls done when each has called done1.
local function all(n, step, done)
  if n == 0 then return done() end
  local left = n
  for i = 1, n do
    step(i, function()
      left = left - 1
      if left == 0 then done() end
    end)
  end
end

-- review_states are a review's state as Oriel says it.
local review_states = { REQUEST_CHANGES = "changes", APPROVED = "approved" }

-- note_of is a Gitea comment as a thread's comment; member is filled in after.
local function note_of(c)
  return { id = tostring(c.id), author = c.user and c.user.login or "", body = c.body or "", at = c.created_at }
end

-- members marks each note whose author is a collaborator on the repo: the agent may read those.
local function members(site, repo, threads, done)
  local who, list = {}, {}
  for _, t in ipairs(threads) do
    for _, n in ipairs(t.comments) do
      if who[n.author] == nil then who[n.author] = false; table.insert(list, n.author) end
    end
  end
  all(#list, function(i, done1)
    L.http.request({ url = site.api .. repo_path(repo) .. "/collaborators/" .. list[i], auth = site.secret, auth_scheme = "token" },
      function(res) who[list[i]] = res ~= nil and res.status == 204; done1() end)
  end, function()
    for _, t in ipairs(threads) do
      for _, n in ipairs(t.comments) do n.member = who[n.author] end
    end
    done(threads)
  end)
end

-- Gitea's merge styles by Oriel's methods.
local styles = { merge = "merge", squash = "squash", rebase = "rebase" }

L.code.host{ name = "gitea", title = "Gitea", order = 40,
  words = { short = "PR", long = "pull request", sigil = "#" },
  sites = { { host = "gitea.com", api = "https://gitea.com/api/v1", secret = "GITEA_TOKEN" } },
  tokens = { env = { "GITEA_TOKEN" } },
  path = [[^/?([^/]+/[^/]+?)(?:\.git)?/?$]],
  takes = [[^/[^/]+/[^/]+/pulls/\d+]],
  setup = { api = "https://{host}/api/v1", secret = "GITEA_TOKEN",
    token_url = "https://{host}/user/settings/applications",
    token_help = "An access token with repository read and write, and user read: Oriel lists, raises and merges your pull requests.",
    probe = function(host, done)
      L.http.request({ url = "https://" .. host .. "/api/v1/version" }, function(res, err)
        done(err == nil and res.status == 200 and res.body:match('"version"') ~= nil)
      end)
    end },

  requested = function(site, done)
    api(site, "GET", "/repos/issues/search?type=pulls&state=open&review_requested=true&limit=50", nil, function(list, err)
      if err then return done(nil, err) end
      local out = {}
      for _, it in ipairs(list) do
        table.insert(out, { url = it.html_url, repo = it.repository.full_name, number = it.number, title = it.title,
          author = it.user and it.user.login, draft = wip(it.title), asked = it.created_at })
      end
      done(out)
    end)
  end,

  lookup = function(site, url, done)
    local repo, n = of_url(url)
    api(site, "GET", repo_path(repo) .. "/pulls/" .. n, nil, function(p, err)
      if err then return done(nil, err) end
      api(site, "GET", repo_path(repo) .. "/pulls/" .. n .. "/files", nil, function(fs)
        local files = {}
        for _, f in ipairs(fs or {}) do table.insert(files, { path = f.filename, adds = f.additions, dels = f.deletions }) end
        done({ request = { url = p.html_url, repo = repo, number = p.number, title = p.title, author = p.user and p.user.login,
            draft = wip(p.title) }, base = p.base.ref, head = p.head.ref, body = p.body,
          mergeable = p.mergeable and "yes" or "conflicts", files = #files > 0 and files or nil,
          clone = "https://" .. site.host .. "/" .. repo .. ".git" })
      end)
    end)
  end,

  checkout = function(site, url, done)
    local repo, n = of_url(url)
    done({ remote = repo, ref = "pull/" .. n .. "/head" })
  end,

  for_branch = function(repo, branch, done)
    api(repo.site, "GET", repo_path(repo.repo) .. "/pulls?state=all&limit=50", nil, function(list, err)
      if err then return done(nil, err) end
      local best
      for _, p in ipairs(list) do
        if p.head.ref == branch and (best == nil or p.state == "open" and best.state ~= "open") then best = p end
      end
      done(best and pr_of(best) or nil)
    end)
  end,

  create = function(repo, new, done)
    local title = new.draft and ("WIP: " .. new.title) or new.title
    api(repo.site, "POST", repo_path(repo.repo) .. "/pulls", { title = title, body = new.body, head = new.branch, base = new.base },
      function(p, err) done(p and pr_of(p), err) end)
  end,

  edit = function(repo, pr, title, body, done)
    if pr.draft and not wip(title) then title = "WIP: " .. title end
    api(repo.site, "PATCH", repo_path(repo.repo) .. "/pulls/" .. pr.number, { title = title, body = body },
      function(p, err) done(p and pr_of(p), err) end)
  end,

  ready = function(repo, pr, done)
    local title = pr.title:gsub("^%s*WIP:%s*", ""):gsub("^%s*%[WIP%]%s*", "")
    api(repo.site, "PATCH", repo_path(repo.repo) .. "/pulls/" .. pr.number, { title = title }, function(_, err) done(nil, err) end)
  end,

  request_reviewers = function(repo, pr, who, done)
    api(repo.site, "POST", repo_path(repo.repo) .. "/pulls/" .. pr.number .. "/requested_reviewers", { reviewers = who },
      function(_, err) done(nil, err) end)
  end,

  checks = function(repo, pr, done) checks(repo.site, repo.repo, pr.head, done) end,

  merge_methods = function(repo, pr, done)
    api(repo.site, "GET", repo_path(repo.repo), nil, function(r, err)
      if err then return done(nil, err) end
      local allowed = {}
      if r.allow_merge_commits then table.insert(allowed, "merge") end
      if r.allow_squash_merge then table.insert(allowed, "squash") end
      if r.allow_rebase then table.insert(allowed, "rebase") end
      local default = styles[r.default_merge_style] and r.default_merge_style or nil
      done({ allowed = allowed, default = default })
    end)
  end,

  blockers = function(repo, pr, done)
    api(repo.site, "GET", repo_path(repo.repo) .. "/pulls/" .. pr.number, nil, function(p, err)
      if err then return done(nil, err) end
      local out = {}
      if wip(p.title) then
        table.insert(out, { kind = "draft", text = "it's a draft: mark it ready first", hard = true })
      end
      if p.mergeable == false then
        table.insert(out, { kind = "conflicts", text = "it conflicts with " .. p.base.ref, hard = true })
      end
      reviews(repo.site, repo.repo, pr.number, function(r, err)
        if err then return done(nil, err) end
        if r.unresolved > 0 then
          local s = r.unresolved == 1 and "" or "s"
          table.insert(out, { kind = "threads", count = r.unresolved, text = r.unresolved .. " reviewer comment" .. s .. " unresolved",
            url = p.html_url .. "/files" })
        end
        if #r.changes > 0 then
          table.insert(out, { kind = "changes", count = #r.changes, text = table.concat(r.changes, ", ") .. " asked for changes", url = p.html_url })
        end
        done(out)
      end)
    end)
  end,

  merge = function(repo, pr, opts, done)
    api(repo.site, "POST", repo_path(repo.repo) .. "/pulls/" .. pr.number .. "/merge",
      { Do = styles[opts.method] or "merge", head_commit_id = opts.head, MergeTitleField = opts.title, MergeMessageField = opts.body,
        delete_branch_after_merge = opts.delete_branch }, function(_, err) done(nil, err) end)
  end,

  -- threads are a thread per review comment (Gitea's API keeps no reply chains), and the conversation (no id).
  threads = function(repo, pr, done)
    local base = repo_path(repo.repo)
    api(repo.site, "GET", base .. "/pulls/" .. pr.number .. "/reviews", nil, function(rs, err)
      if err then return done(nil, err) end
      local threads = {}
      all(#rs, function(i, done1)
        local r = rs[i]
        if (r.comments_count or 0) == 0 then return done1() end
        api(repo.site, "GET", base .. "/pulls/" .. pr.number .. "/reviews/" .. r.id .. "/comments", nil, function(cs)
          for _, c in ipairs(cs or {}) do
            table.insert(threads, { id = tostring(c.id), path = c.path, line = c.position or c.original_position,
              resolved = c.resolver ~= nil, reviewer = r.user and r.user.login, state = review_states[r.state] or "commented",
              url = c.html_url, comments = { note_of(c) } })
          end
          done1()
        end)
      end, function()
        api(repo.site, "GET", base .. "/issues/" .. pr.number .. "/comments", nil, function(cs, err)
          if err then return done(nil, err) end
          local convo = { url = pr.url, comments = {} }
          for _, c in ipairs(cs) do table.insert(convo.comments, note_of(c)) end
          if #convo.comments > 0 then table.insert(threads, convo) end
          members(repo.site, repo.repo, threads, done)
        end)
      end)
    end)
  end,

  -- reply posts to the conversation: Gitea's API can't reply in a review comment's thread, so a reply to one quotes
  -- the line it's on.
  reply = function(repo, pr, thread, body, done)
    if thread.id and thread.id ~= "" then
      body = "> On `" .. (thread.path or "?") .. ":" .. (thread.line or 0) .. "` (review comment " .. thread.id .. ")\n\n" .. body
    end
    api(repo.site, "POST", repo_path(repo.repo) .. "/issues/" .. pr.number .. "/comments", { body = body },
      function(c, err) done(c and tostring(c.id), err) end)
  end,
}

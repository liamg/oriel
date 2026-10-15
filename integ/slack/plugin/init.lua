-- Slack PTAL: a raised pull request's Slack button posts "PTAL", its title and its link to your channel through an
-- incoming webhook. The webhook's URL is the secret SLACK_WEBHOOK: this plugin names it, and the host sends it to
-- hooks.slack.com alone.

local SECRET = "SLACK_WEBHOOK"
local TITLE_CAP = 160 -- keeps the message to one line

local posts = {}   -- how many times each PR has been posted, by its key
local sending = {} -- the PRs being posted now

local function trim(s)
  return ((s or ""):gsub("^%s+", ""):gsub("%s+$", ""))
end

-- pr_ref is "owner/name#number" for a pull request's URL, nil for any other.
local function pr_ref(url)
  local owner, name, n, rest = url:match("^%a[%w+.-]*://[^/]+/([^/]+)/([^/]+)/pull/(%d+)(.*)$")
  if not owner or not (rest == "" or rest:match("^[/?#]")) then
    return nil
  end
  return owner .. "/" .. name .. "#" .. n
end

-- key is a PR's identity for counting its posts.
local function key(url)
  url = trim(url)
  return (pr_ref(url) or url):lower()
end

-- escape hides what Slack reads as its own markup.
local function escape(s)
  return (s:gsub("&", "&amp;"):gsub("<", "&lt;"):gsub(">", "&gt;"))
end

-- truncate cuts s to n characters, not bytes.
local function truncate(s, n)
  local chars = {}
  for ch in s:gmatch("[^\128-\191][\128-\191]*") do
    chars[#chars + 1] = ch
  end
  if #chars <= n then
    return s
  end
  return (table.concat(chars, "", 1, n):gsub("%s+$", "")) .. "…"
end

local function pr_text(url, title)
  url = trim(url)
  title = truncate(trim(title), TITLE_CAP)
  local link = url
  local ref = pr_ref(url)
  if ref then
    link = "<" .. url .. "|" .. escape(ref) .. ">"
  elseif url ~= "" then
    link = "<" .. url .. ">"
  end
  if title == "" then
    return "PTAL: " .. link
  end
  return "PTAL: *" .. escape(title) .. "* — " .. link
end

local function ordinal(n)
  local suffix = "th"
  if n % 100 < 11 or n % 100 > 13 then
    suffix = ({ [1] = "st", [2] = "nd", [3] = "rd" })[n % 10] or "th"
  end
  return n .. suffix
end

-- bump_text is the k-th repeat of a post: a nudge, not a new PR.
local function bump_text(k, text)
  if k <= 0 then
    return text
  elseif k == 1 then
    return "Bump! " .. text
  end
  return ordinal(k) .. " Bump! " .. text
end

-- emoji is what Slack's icon_emoji takes: a name in colons; a pasted emoji character isn't one.
local function emoji(s)
  s = trim(s):gsub("^:+", ""):gsub(":+$", "")
  if s == "" or s:find("[^%w_+-]") then
    return nil
  end
  return ":" .. s .. ":"
end

local function channel(s)
  s = trim(s)
  if s == "" then
    return nil
  end
  if s:sub(1, 1) == "#" or s:sub(1, 1) == "@" then
    return s
  end
  return "#" .. s
end

oriel.command("slack.ptal", function(a)
  local url = trim(a.url)
  if url == "" then
    error("slack.ptal needs the pull request's url")
  end
  local k = key(url)
  if sending[k] then
    return
  end
  local prior = posts[k] or 0
  local payload = { text = bump_text(prior, pr_text(url, a.title)) }
  payload.username = trim(a.username) ~= "" and trim(a.username) or nil
  payload.icon_emoji = emoji(a.emoji)
  payload.channel = channel(a.channel)
  sending[k] = true
  oriel.http.request({
    method = "POST",
    url_secret = SECRET,
    headers = { ["Content-Type"] = "application/json" },
    body = oriel.json(payload),
    timeout = 15,
  }, function(res, err)
    sending[k] = nil
    if err then
      oriel.notify("Slack: " .. err, "error")
      return
    end
    if res.status < 200 or res.status >= 300 then
      local reason = trim(res.body)
      if reason == "" then
        reason = tostring(res.status)
      end
      oriel.notify("Slack refused the message: " .. reason, "error")
      return
    end
    posts[k] = prior + 1
    local where = payload.channel and (" to " .. payload.channel) or " to Slack"
    if posts[k] > 1 then
      oriel.notify("Bump " .. (posts[k] - 1) .. " posted" .. where)
    else
      oriel.notify("Posted" .. where)
    end
  end)
end, { desc = "Ask your Slack channel to take a look at a pull request: { url, title; channel, username, emoji }" })

oriel.command("slack.webhook", function()
  oriel.cmd("secrets.set", { name = SECRET })
end, { desc = "Set the Slack incoming webhook PTAL posts through" })

oriel.ship.action({
  name = "slack",
  order = 60,
  command = "slack.ptal",
  label = function(pr)
    if pr.state ~= "open" or pr.merged or trim(pr.url) == "" then
      return nil
    end
    local k = key(pr.url)
    if sending[k] then
      return "Sending…"
    end
    local n = posts[k] or 0
    if n == 0 then
      return "Slack"
    elseif n == 1 then
      return "Bump Slack"
    end
    return "Bump " .. n
  end,
})

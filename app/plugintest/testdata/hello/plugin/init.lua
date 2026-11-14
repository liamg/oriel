local L = oriel

local count = 0
local proc = L.process.spawn("bin/oriel-hello", {})

local function greeting()
  return L.option["plugin.hello.greeting"] or "hello"
end

L.option{ name = "plugin.hello.greeting", type = "string", default = "hello", help = "What it says",
  on_change = function() L.hud.refresh("hello") end }

local function more()
  count = count + 1
  L.hud.refresh("hello")
end

L.command("hello.count", more)

L.hud.indicator("hello", function()
  return L.ui.row{
    L.ui.button{ id = "more", label = "More", on_click = more },
    L.ui.text(greeting() .. " · " .. count),
    L.ui.text(os.date("!%H:%M:%S", math.floor(L.now()))),
    gap = 8,
  }
end, { slot = "inbox.foot", every = "1s" })

L.hud.indicator("hello.say", function()
  local draft = L.ui.state("hello.say", { text = "" })
  return L.ui.input{ id = "say", value = draft.text, placeholder = "say something…", label = "Greeting",
    on_change = function(t)
      draft.text = t
      L.hud.refresh("hello.say")
    end,
    on_submit = function(t) L.option["plugin.hello.greeting"] = t end }
end, { slot = "hud", title = "HELLO" })

L.agent.tool{ name = "greet", ["for"] = { "assistant" }, read_only = true, async = true, desc = "Greet someone.",
  params = { who = "string" },
  run = function(p, done)
    proc:request("echo", { text = greeting() .. ", " .. p.who }, function(res, err)
      if err then
        return done(nil, tostring(err))
      end
      done({ said = res.text, count = count })
    end)
  end }

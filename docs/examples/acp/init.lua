-- Gemini CLI as an agent: the bundled ACP bridge runs `gemini --experimental-acp` for each session, in its sandbox
-- when it has one, and is its Agent Client Protocol client. Sign in by running gemini once in a terminal.
oriel.agent.provider{
  name = "gemini",
  title = "Gemini",
  mark = "G",
  order = 30,
  acp = {
    command = { "gemini", "--experimental-acp" },
    login = "gemini",
    -- In a sandbox: what it reaches, the variables it inherits, and where it keeps its sign-in.
    hosts = { "generativelanguage.googleapis.com", "oauth2.googleapis.com", "cloudcode-pa.googleapis.com" },
    tools = { "node@22", "npm:@google/gemini-cli@0.9.0" }, -- a container installs these to run it
    pass_env = { "GEMINI_*", "GOOGLE_*" },
    home = { ".gemini" },
    models = {
      { id = "gemini-2.5-pro", name = "Gemini 2.5 Pro", default = true },
      { id = "gemini-2.5-flash", name = "Gemini 2.5 Flash", small = true },
    },
  },
}

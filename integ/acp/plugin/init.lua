-- The ACP bridge: its process (bin/oriel-acp, oriel's own plugin-process acp) serves an Agent Client Protocol agent
-- over the Oriel agent protocol. A plugin brings one with oriel.agent.provider{ name = 'gemini', acp = { command =
-- { 'gemini', '--experimental-acp' } } }, and its exec grant for the command.
oriel.agent.bridge{ name = "acp", title = "Agent Client Protocol", program = "bin/oriel-acp" }

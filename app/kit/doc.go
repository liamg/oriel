// Package kit is what every one of the app's features shares: fonts, theme tokens, formatters, session and git
// helpers, the cross-feature services' contracts (Reviews, Inbox, Explains…) and the usual plugin plumbing. What only
// some share has a package of its own under it: kit/work (work off the loop, polling), kit/sessionkit (the session
// host's parts, session args), kit/prui (pull request views), kit/diagrams (mermaid diagrams), kit/agentkit (agent
// pickers); general controls belong in ui/widget.
package kit

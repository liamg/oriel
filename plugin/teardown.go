package plugin

import "context"

// Teardown is the slow part of a plugin's Deactivate, given its ctx: a language server's shutdown, a wait for git.
// Plugins stop as the app quits, and it waits for this before it exits.
func Teardown(ctx context.Context, fn func(context.Context)) { fn(ctx) }

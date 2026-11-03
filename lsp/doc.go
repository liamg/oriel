// Package lsp is the LSP client, server registry and capabilities: a Client per running server, a Manager that
// starts servers as files open and routes to them, and the built-in ServerSpecs. Its types use Oriel's
// positions (byte columns); the encoding the server speaks stays inside.
package lsp

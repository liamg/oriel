// Package lang is the editor's language tooling: language servers (LSP), their diagnostics as Problems, the
// debugger (Debug, over DAP) and the test runner (Tests). Each is a plugin of its own; they share a package because
// they share the language machinery (registry/lang's servers, adapters and test kinds).
package lang

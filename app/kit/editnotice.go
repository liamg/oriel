package kit

import (
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/edit"
)

// FromEdit is an engine's notice as a core one. Source is the engine: "vim", "plain".
func FromEdit(n edit.Notice, source string) core.Notice {
	sev := map[edit.Severity]core.Severity{edit.Info: core.SevInfo, edit.Warning: core.SevWarning, edit.Error: core.SevError,
		edit.Question: core.SevQuestion}[n.Severity]
	return core.Notice{ID: n.ID, Severity: sev, Source: source, Text: n.Text, Key: true}
}

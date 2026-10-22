package hostcore

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
)

// JSON schema types for a tool's properties.
const (
	StrType  = `{"type":"string"}`
	BoolType = `{"type":"boolean"}`
	IntType  = `{"type":"integer"}`
)

// Schema is a tool's input schema: an object of props, with required ones.
func Schema(props string, required ...string) json.RawMessage {
	req, _ := json.Marshal(required)
	if required == nil {
		req = []byte("[]")
	}
	return json.RawMessage(`{"type":"object","properties":{` + props + `},"required":` + string(req) + `}`)
}

// Args are a tool call's arguments, read leniently.
type Args map[string]any

// ParseArgs reads a tool call's arguments.
func ParseArgs(raw json.RawMessage) Args {
	var a Args
	_ = json.Unmarshal(raw, &a)
	return a
}

func (a Args) Str(k string) string {
	switch v := a[k].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func (a Args) Int(k string) int {
	switch v := a[k].(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func (a Args) Bool(k string) bool { b, _ := a[k].(bool); return b }

// ProjectsTool is the projects tool's answer for a session: each project's name, directory, branch, and whether it
// has uncommitted changes.
func ProjectsTool(ctx context.Context, se core.SessionState) (string, error) {
	type row struct {
		Name     string `json:"name"`
		Dir      string `json:"dir"`
		Branch   string `json:"branch,omitempty"`
		Base     string `json:"base,omitempty"`
		Primary  bool   `json:"primary,omitempty"`
		ReadOnly bool   `json:"readOnly,omitempty"`
		Dirty    bool   `json:"uncommitted,omitempty"`
	}
	var out []row
	for _, p := range se.Projects {
		out = append(out, row{Name: p.Name, Dir: p.Dir, Branch: p.Branch, Base: p.Base, Primary: p.Primary, ReadOnly: p.ReadOnly,
			Dirty: !p.ReadOnly && gitx.Repo{Dir: p.Dir}.Dirty(ctx)})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	return string(b), err
}

// DefaultModel is a provider's default model, else its first.
func DefaultModel(ctx context.Context, p agent.Provider) string {
	ms, err := p.Models(ctx)
	if err != nil || len(ms) == 0 {
		return ""
	}
	for _, m := range ms {
		if m.Default {
			return m.ID
		}
	}
	return ms[0].ID
}

// SignedIn reports whether a provider is installed and signed in, as it says itself.
func SignedIn(ctx context.Context, p agent.Provider) bool {
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	in, err := p.Detect(dctx)
	return err == nil && in.LoggedIn
}

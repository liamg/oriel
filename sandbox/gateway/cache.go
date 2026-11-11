package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/liamg/oriel/sandbox/mcp"
)

// toolCache is a server's tools as last listed. It holds a hash of the server's config, never the config: that may
// carry secrets.
type toolCache struct {
	Key   string     `json:"key"`
	Tools []mcp.Tool `json:"tools"`
}

// cacheKey changes whenever what s starts or reaches does.
func cacheKey(s Server) string {
	b, _ := json.Marshal(struct {
		Type, Command, URL string
		Args               []string
		Env, Headers       map[string]string
	}{s.Type, s.Command, s.URL, s.Args, s.Env, s.Headers})
	sum := sha256.Sum256(append([]byte(s.Name+"\x00"), b...))
	return hex.EncodeToString(sum[:])
}

func (u *Upstreams) cachePath(s Server) string { return filepath.Join(u.Cache, s.Name+".json") }

// cached is s's tool list as kept for its config now.
func (u *Upstreams) cached(s Server) ([]mcp.Tool, bool) {
	if u.Cache == "" {
		return nil, false
	}
	b, err := os.ReadFile(u.cachePath(s))
	if err != nil {
		return nil, false
	}
	var c toolCache
	if json.Unmarshal(b, &c) != nil || c.Key != cacheKey(s) {
		return nil, false
	}
	return c.Tools, true
}

// keep saves s's tool list for its config; a failure only means the next sandbox lists it afresh.
func (u *Upstreams) keep(s Server, tools []mcp.Tool) {
	if u.Cache == "" {
		return
	}
	b, err := json.Marshal(toolCache{Key: cacheKey(s), Tools: tools})
	if err != nil || os.MkdirAll(u.Cache, 0o700) != nil {
		return
	}
	f, err := os.CreateTemp(u.Cache, s.Name+".*.tmp")
	if err != nil {
		return
	}
	_, werr := f.Write(b)
	cerr := f.Close()
	if werr != nil || cerr != nil || os.Chmod(f.Name(), 0o600) != nil || os.Rename(f.Name(), u.cachePath(s)) != nil {
		_ = os.Remove(f.Name())
	}
}

func sameTools(a, b []mcp.Tool) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

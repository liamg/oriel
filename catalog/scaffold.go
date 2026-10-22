package catalog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Scaffold writes a new plugin into dir (oriel plugin init): its oriel.toml and init.lua, and with process a Go
// command under cmd/<name> that init.lua spawns, speaking JSON-RPC over stdio. It writes over nothing.
func Scaffold(dir, id string, process bool) ([]string, error) {
	owner, name, ok := strings.Cut(id, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return nil, fmt.Errorf("the id %q isn't <owner>/<name>", id)
	}
	files := map[string]string{
		"oriel.toml": fmt.Sprintf(`id          = %q
version     = "0.1.0"
oriel       = ">=0.9"
description = ""
license     = "MIT"
`, id),
		"init.lua": fmt.Sprintf(`local L = oriel

L.command(%q, function()
  L.notify("Hello from %s")
end)
`, title(name), id),
	}
	if process {
		files["oriel.toml"] += `binaries    = ["darwin/arm64", "darwin/amd64", "linux/amd64", "linux/arm64"]
`
		files["init.lua"] = fmt.Sprintf(`local L = oriel

-- The plugin's own process: no exec grant needed, OS-sandboxed.
local proc = L.process.spawn("bin/%s")

L.command(%q, function()
  proc:request("hello", { name = "Oriel" }, function(res, err)
    L.notify(err or res.text)
  end)
end)
`, name, title(name))
		files[".gitignore"] = "/dist/\n/bin/\n"
		files["go.mod"] = fmt.Sprintf("module github.com/%s/oriel-%s\n\ngo 1.26.0\n", owner, name)
		files[filepath.Join("cmd", name, "main.go")] = `// Command ` + name + ` is the plugin's process: JSON-RPC 2.0 over stdio, a message a line.
package main

import (
	"bufio"
	"encoding/json"
	"os"
)

type request struct {
	ID     json.RawMessage ` + "`json:\"id\"`" + `
	Method string          ` + "`json:\"method\"`" + `
	Params json.RawMessage ` + "`json:\"params\"`" + `
}

func main() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(nil, 16<<20)
	out := json.NewEncoder(os.Stdout)
	for in.Scan() {
		var r request
		if json.Unmarshal(in.Bytes(), &r) != nil || r.ID == nil {
			continue
		}
		reply := map[string]any{"jsonrpc": "2.0", "id": r.ID}
		switch r.Method {
		case "hello":
			var p struct{ Name string }
			json.Unmarshal(r.Params, &p)
			reply["result"] = map[string]string{"text": "Hello, " + p.Name}
		default:
			reply["error"] = map[string]any{"code": -32601, "message": "no method " + r.Method}
		}
		out.Encode(reply)
	}
}
`
	}
	var written []string
	for f := range files {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return nil, fmt.Errorf("%s is there already", f)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	for f, body := range files {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return nil, err
		}
		written = append(written, f)
	}
	return sorted(written), nil
}

// title is a command name from a plugin name: my-thing → MyThing.
func title(name string) string {
	var b strings.Builder
	for _, w := range strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' || r == '.' }) {
		b.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	return b.String()
}

package watches

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/watch"
)

// builtins are the sources subscribed to by name (oriel.subscribe): files, and a work tree's git.
func (x *Watches) builtins() []registry.Named {
	tree := func(id core.SessionID, args map[string]any) (string, error) {
		dir, _ := args["dir"].(string)
		if dir == "" {
			return "", errors.New("no dir: the work tree to read")
		}
		root, ok := gitx.Root(context.Background(), dir)
		if !ok {
			return "", fmt.Errorf("%s isn't in a git work tree", dir)
		}
		return root, nil
	}
	status := func(name string, counted bool) registry.Named {
		return registry.Named{Name: name, Doc: "a work tree's git status { branch, head, base, upstream, ahead, behind, files = { { path, code, area, add, del } } }: dir",
			Make: func(id core.SessionID, args map[string]any) (watch.Source[any], error) {
				root, err := tree(id, args)
				if err != nil {
					return watch.Source[any]{}, err
				}
				t := x.Tree(id, root)
				src := t.Status()
				if counted {
					src = t.Counted()
				}
				return watch.Derive(src.Key+":lua", src, func(st *gitx.State) (any, error) { return statusValue(st), nil }), nil
			}}
	}
	return []registry.Named{
		{Name: "files", Doc: "the paths that changed under path (tree = true for everything under a folder)",
			Make: func(id core.SessionID, args map[string]any) (watch.Source[any], error) {
				path, _ := args["path"].(string)
				if path == "" {
					return watch.Source[any]{}, errors.New("no path to watch")
				}
				deep, _ := args["tree"].(bool)
				path = filepath.Clean(path)
				src := watch.FilesSource(fmt.Sprintf("files:%s:%v", path, deep), watch.Files{Path: path, Tree: deep})
				return watch.Derive(src.Key+":lua", src, func(ps []string) (any, error) {
					out := make([]any, len(ps))
					for i, p := range ps {
						out[i] = p
					}
					return out, nil
				}), nil
			}},
		status("git.status", false),
		status("git.counted", true),
		{Name: "git.remote", Doc: "a work tree's origin URL: dir",
			Make: func(id core.SessionID, args map[string]any) (watch.Source[any], error) {
				root, err := tree(id, args)
				if err != nil {
					return watch.Source[any]{}, err
				}
				src := x.Tree(id, root).Remote()
				return watch.Derive(src.Key+":lua", src, func(u string) (any, error) { return u, nil }), nil
			}},
	}
}

// statusValue is a status as plain values, for Lua.
func statusValue(st *gitx.State) map[string]any {
	if st == nil {
		return nil
	}
	var files []any
	for _, f := range st.Files(gitx.Changed) {
		files = append(files, map[string]any{"path": f.Path, "old": f.Old, "code": string(rune(f.Code)), "area": f.Area.String(),
			"add": float64(f.Add), "del": float64(f.Del), "binary": f.Binary})
	}
	var unc []any
	for _, f := range st.Files(gitx.Uncommitted) {
		unc = append(unc, map[string]any{"path": f.Path, "code": string(rune(f.Code)), "area": f.Area.String(), "add": float64(f.Add), "del": float64(f.Del)})
	}
	return map[string]any{"branch": st.Branch, "head": st.Head, "base": st.Base, "upstream": st.Upstream, "ahead": float64(st.Ahead),
		"behind": float64(st.Behind), "files": files, "uncommitted": unc}
}

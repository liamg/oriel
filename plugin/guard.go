package plugin

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Grant is one declared capability: all of a plugin's are accepted together at install.
type Grant struct {
	Cap   Capability
	Scope []string // hosts, paths, commands, tools or secret names, as the capability takes
	Why   string   // the author's reason, shown as theirs
}

// ErrNotDeclared is what every undeclared call fails with (errors.Is), as a *NotDeclared naming what's missing.
var ErrNotDeclared = errors.New("capability not declared")

// NotDeclared is a call a plugin's manifest doesn't allow.
type NotDeclared struct {
	Plugin string
	Cap    Capability
	Target string // the host, path, command or name it was for; "" for the capability itself
}

func (e *NotDeclared) Error() string {
	if e.Target == "" {
		return fmt.Sprintf("%s: the %q capability isn't declared in its oriel.toml", e.Plugin, e.Cap)
	}
	return fmt.Sprintf("%s: the %q capability isn't declared for %s in its oriel.toml", e.Plugin, e.Cap, e.Target)
}

func (e *NotDeclared) Is(target error) bool { return target == ErrNotDeclared }

// Use is one capability check, kept for the audit log.
type Use struct {
	Time    time.Time
	Plugin  string
	Cap     Capability
	Target  string
	Allowed bool
	Why     string // a grant widened at run time (Widen): what it was widened for
}

// Guard is the check every host API call from a plugin goes through.
type Guard interface {
	Check(plugin string, c Capability, target string) error // nil, or a *NotDeclared
	Audit() []Use
	Declared(plugin string) []Grant // what the plugin was granted, to build its OS sandbox from
	// Widen grants the plugin gs at run time, beyond what it declared, for why, which the audit records; undo takes
	// exactly gs back.
	Widen(plugin, why string, gs ...Grant) (undo func())
}

// AuditSize is how many uses Grants keeps.
const AuditSize = 1000

// Grants is the Guard: what each plugin declared and was granted at install.
type Grants struct {
	// Places resolves a named fs scope ("worktree", "config") to directories; nil knows none.
	Places func(name string) []string

	mu     sync.Mutex
	grants map[string][]Grant
	audit  []Use
}

// NewGrants is a guard that grants nothing yet.
func NewGrants() *Grants { return &Grants{grants: map[string][]Grant{}} }

// Grant records what plugin may do, replacing what it had.
func (g *Grants) Grant(plugin string, gs []Grant) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.grants[plugin] = gs
}

// Add grants plugin more, at runtime: a self-hosted code host's server, once the user sets it up.
func (g *Grants) Add(plugin string, gs ...Grant) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.grants[plugin] = append(g.grants[plugin], gs...)
}

// Widen is Guard.Widen.
func (g *Grants) Widen(plugin, why string, gs ...Grant) (undo func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.grants[plugin] = append(g.grants[plugin], gs...)
	for _, gr := range gs {
		g.audit = append(g.audit, Use{Time: time.Now(), Plugin: plugin, Cap: gr.Cap, Target: strings.Join(gr.Scope, ", "), Allowed: true, Why: why})
	}
	if len(g.audit) > AuditSize {
		g.audit = g.audit[len(g.audit)-AuditSize:]
	}
	return sync.OnceFunc(func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		for _, gr := range gs {
			if i := slices.IndexFunc(g.grants[plugin], func(x Grant) bool { return x.Cap == gr.Cap && slices.Equal(x.Scope, gr.Scope) && x.Why == gr.Why }); i >= 0 {
				g.grants[plugin] = slices.Delete(g.grants[plugin], i, i+1)
			}
		}
	})
}

// Revoke takes everything from plugin.
func (g *Grants) Revoke(plugin string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.grants, plugin)
}

func (g *Grants) Declared(plugin string) []Grant {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Grant(nil), g.grants[plugin]...)
}

func (g *Grants) Check(plugin string, c Capability, target string) error {
	ok := c.Always()
	if !ok {
		for _, gr := range g.Declared(plugin) {
			if gr.Cap == c && g.covers(gr, target) {
				ok = true
				break
			}
		}
	}
	g.mu.Lock()
	g.audit = append(g.audit, Use{Time: time.Now(), Plugin: plugin, Cap: c, Target: target, Allowed: ok})
	if len(g.audit) > AuditSize {
		g.audit = g.audit[len(g.audit)-AuditSize:]
	}
	g.mu.Unlock()
	if !ok {
		return &NotDeclared{Plugin: plugin, Cap: c, Target: target}
	}
	return nil
}

func (g *Grants) Audit() []Use {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Use(nil), g.audit...)
}

// covers says whether a grant reaches target: any target of its capability when target is "".
func (g *Grants) covers(gr Grant, target string) bool {
	info, _ := gr.Cap.Info()
	if target == "" || info.Scope == "" {
		return true
	}
	for _, s := range gr.Scope {
		if s == "*" {
			return true
		}
		switch info.Scope {
		case "hosts":
			if matchHost(s, target) {
				return true
			}
		case "paths":
			for _, dir := range g.places(s) {
				if Within(dir, target) {
					return true
				}
			}
		case "commands":
			if matchCommand(s, target) {
				return true
			}
		case "names":
			if matchName(s, target) {
				return true
			}
		default:
			if s == target {
				return true
			}
		}
	}
	return false
}

func (g *Grants) places(s string) []string {
	if s == "worktree" || s == "config" {
		if g.Places == nil {
			return nil
		}
		var out []string
		for _, d := range g.Places(s) {
			out = append(out, RealPath(d))
		}
		return out
	}
	return []string{RealPath(Expand(s))}
}

// Expand is a path with ~ as the home directory.
func Expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// Within says whether path is dir or under it; both absolute.
func Within(dir, path string) bool {
	if dir == "" || !filepath.IsAbs(dir) || !filepath.IsAbs(path) {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// matchHost is a host pattern ("api.linear.app", "*.atlassian.net", "host:8080") against a host, with or
// without its port. A pattern with no port takes any.
func matchHost(pat, target string) bool {
	ph, pp := splitHost(pat)
	th, tp := splitHost(target)
	if pp != "" && pp != tp {
		return false
	}
	ph, th = strings.ToLower(ph), strings.ToLower(th)
	if rest, ok := strings.CutPrefix(ph, "*."); ok {
		return strings.HasSuffix(th, "."+rest)
	}
	return ph == th
}

// matchName is a declared secret ("TOKEN@api.linear.app") against a use of it: its name alone, or "NAME@host"
// for the host it's about to be sent to, which must be the one it's bound to.
func matchName(decl, target string) bool {
	dn, dh, _ := strings.Cut(decl, "@")
	tn, th, sent := strings.Cut(target, "@")
	if dn != tn {
		return false
	}
	return !sent || dh != "" && matchHost(dh, th)
}

func splitHost(s string) (host, port string) {
	if h, p, err := net.SplitHostPort(s); err == nil {
		return h, p
	}
	return s, ""
}

// matchCommand is a declared command against a program: the same name or path, or, for a bare name, the program
// it finds on PATH. A bare name never matches another program of that name elsewhere.
func matchCommand(decl, target string) bool {
	if decl == target {
		return true
	}
	if !filepath.IsAbs(target) {
		return false
	}
	if strings.ContainsRune(decl, filepath.Separator) {
		return RealPath(Expand(decl)) == RealPath(target)
	}
	p, err := exec.LookPath(decl)
	return err == nil && RealPath(p) == RealPath(target)
}

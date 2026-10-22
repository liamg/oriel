package sandbox

import (
	"os"
	"path/filepath"
)

// ProjectMemory is where an agent keeps what it learns about one project.
type ProjectMemory struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`
}

// MemoryDir is the memory directory for a repository on the host, which every session in it shares: where Claude
// Code's auto memory keeps it, so a session outside Oriel reads it too.
func MemoryDir(repo string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects", ProjectDirName(filepath.Clean(repo)), "memory")
}

// MemoryDirs are root's projects' memory directories, the primary's first; none for a workspace with no projects.
func MemoryDirs(root string) []ProjectMemory {
	ws, _ := LoadManifest(root)
	return ws.Memory
}

// memoryBinds create root's memory directories and give the agent each one read-write.
func memoryBinds(root string) ([]Bind, error) {
	var b []Bind
	for _, m := range MemoryDirs(root) {
		if err := os.MkdirAll(m.Dir, 0o700); err != nil {
			return nil, err
		}
		b = append(b, Bind{Host: m.Dir, Agent: m.Dir})
	}
	return b, nil
}

package sandbox

import (
	"encoding/json"
	"os"
)

// Project is one repository worktree in a sandboxed workspace, as the host
// recorded it.
type Project struct {
	Name string `json:"name"`
	// Repo is the original checkout the worktree belongs to.
	Repo string `json:"repo"`
	// Dir is the worktree, relative to the workspace root.
	Dir    string `json:"dir"`
	Branch string `json:"branch,omitempty"`
	// Primary marks the repository the workspace was created from.
	Primary bool `json:"primary,omitempty"`
}

// Manifest is the authoritative copy of a sandboxed workspace's projects,
// kept under Dir where the container cannot change it.
type Manifest struct {
	Projects []Project `json:"projects"`
}

// LoadManifest reads root's manifest; ok=false when there is none.
func LoadManifest(root string) (Manifest, bool) {
	b, err := os.ReadFile(ManifestPath(root))
	if err != nil {
		return Manifest{}, false
	}
	var m Manifest
	if json.Unmarshal(b, &m) != nil {
		return Manifest{}, false
	}
	return m, true
}

// SaveManifest writes root's manifest, readable by the user only.
func SaveManifest(root string, m Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(Dir(root), 0o700); err != nil {
		return err
	}
	return os.WriteFile(ManifestPath(root), b, 0o600)
}

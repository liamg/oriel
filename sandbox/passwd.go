package sandbox

import (
	"fmt"
	"strings"
)

// Identity is the host user the container runs as.
type Identity struct {
	Name  string
	UID   int
	GID   int
	Group string
	Home  string
}

// Passwd appends the host user to an image's /etc/passwd, replacing any entry
// with the same name or uid so the host identity wins.
func Passwd(image string, id Identity) string {
	return mergeEntries(image, id.Name, id.UID, fmt.Sprintf("%s:x:%d:%d::%s:/bin/bash", id.Name, id.UID, id.GID, id.Home))
}

// Group does the same for /etc/group.
func Group(image string, id Identity) string {
	name := id.Group
	if name == "" {
		name = id.Name
	}
	return mergeEntries(image, name, id.GID, fmt.Sprintf("%s:x:%d:", name, id.GID))
}

func mergeEntries(image, name string, id int, entry string) string {
	var b strings.Builder
	num := fmt.Sprint(id)
	for _, line := range strings.Split(image, "\n") {
		f := strings.Split(line, ":")
		if strings.TrimSpace(line) == "" || (len(f) > 2 && (f[0] == name || f[2] == num)) {
			continue
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(entry + "\n")
	return b.String()
}

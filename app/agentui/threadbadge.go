package agentui

import (
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry/uireg"
)

// commentBadge marks the Explorer's files with their open threads: the agent's, yours, and a code host's reviewers'.
func (x *Requests) commentBadge() uireg.FileBadge {
	return uireg.FileBadge{Name: "comments", Glyph: "▪", Tone: "accent", Noun: "comment", Help: "files/explorer#comments", Order: 10,
		Counts: func(id core.SessionID) map[string]int {
			s, ok := x.h.Session(id)
			if !ok {
				return nil
			}
			return threadCounts(s.Snapshot())
		}}
}

// threadCounts are a session's open threads on files, by the file's absolute path.
func threadCounts(se core.SessionState) map[string]int {
	out := map[string]int{}
	for _, t := range se.Threads {
		if !t.Open() || t.Btw || t.Anchor == nil || t.Anchor.File == "" || t.Anchor.Project == core.DocsProject {
			continue
		}
		out[kit.AnchorPath(se, t.Anchor)]++
	}
	return out
}

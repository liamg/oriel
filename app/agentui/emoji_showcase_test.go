//go:build showcase

package agentui_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// An agent's turn full of emoji and symbols, in its console: $ORIEL_SHOTS/emoji-console.png.
func TestShowcaseEmoji(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Now = apptest.At(14, 30, 0)
	ago := func(sec int) time.Time { return s.Now.Add(-time.Duration(sec) * time.Second) }
	se := core.SessionState{ID: "release", Name: "release v0.0.12 🚀", Kind: core.KindWork, Status: core.StatusQuiet,
		Projects: []core.Project{{Name: "oriel", Dir: "/src/oriel", Branch: "release", Primary: true}},
		Agent:    core.AgentState{Provider: "claude", Model: "claude-opus-5-5", Running: true},
		Transcript: []core.Item{
			{ID: "u", Kind: core.ItemUser, Time: ago(60), Text: "How did the release go? 🚀"},
			{ID: "b", Kind: core.ItemTool, Tool: "Bash", Title: "Bash make check ✓", Done: true, Time: ago(50), Duration: 2300,
				Output: "⏺ vet ✔\n  ⎿ lint ✔\n✻ tests ✔ (412 passed)"},
			{ID: "a", Kind: core.ItemAssistant, Time: ago(40), Text: "## Release v0.0.12 🎉\n\n" +
				"- ✅ **Build**: macOS and Linux green 🟢\n" +
				"- ⚠️ One flaky test on macOS, passed on the re-run 🔁\n" +
				"- 🐛 Fixed the Linear freeze: 🧊 → 🔥\n" +
				"- People and sequences: 👍🏽 👨‍💻 👩🏾‍🚀 🧑‍🤝‍🧑 🏳️‍🌈 🇬🇧 1️⃣ #️⃣ ❤️\n" +
				"- Newest emoji (Emoji 16 and 17): 🫩 🫆 🪾 🫪 🫯 🫈\n" +
				"- Symbols in the text's weight: ★ ☆ ● ○ ◆ ▶ ☐ ☑ ☒ ⌘ ⌥ ⇧ ⏎ ⎋ → ⇒ …"},
		},
	}
	for _, ev := range []core.Event{core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID}} {
		if err := s.Store().Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	s.Run("view.open", registry.Args{"id": kit.AgentConsoleView, "place": "tab", "title": "Agent"})
	s.Frames(2)
	s.Shot("emoji-console")
}

// The same in the editor's monospaced face: $ORIEL_SHOTS/emoji-editor.png.
func TestShowcaseEmojiInTheEditor(t *testing.T) {
	dir := t.TempDir()
	src := "# Release notes 🎉\n\n" +
		"// ✅ build  ⚠️ flaky  🐛 fixed  🚀 shipped\n" +
		"// 👍🏽 👨‍💻 👩🏾‍🚀 🧑‍🤝‍🧑 🏳️‍🌈 🇬🇧 1️⃣ #️⃣ ❤️\n" +
		"// Emoji 16 and 17: 🫩 🫆 🪾 🫪 🫯 🫈\n" +
		"// ⏺ Bash(make check)\n//   ⎿ ok ✔  ✻ ✳ ✢  ★ ☆ ☐ ☑ ⌘ ⌥ ⇧ ⏎ → ⇒\n"
	if err := os.WriteFile(filepath.Join(dir, "NOTES.txt"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s := apptest.New(t, apptest.Options{WorkDir: dir, Size: apptest.MockupSize})
	s.Run("editor.open", registry.Args{"path": "NOTES.txt"})
	s.Frames(3)
	s.Shot("emoji-editor")
}

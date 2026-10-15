package voice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Speaker says text out loud.
type Speaker interface {
	Speak(ctx context.Context, text string) error
}

// Run runs a program with stdin; swapped in tests.
type Run func(ctx context.Context, stdin, name string, args ...string) error

func run(ctx context.Context, stdin, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %s", filepath.Base(name), firstLine(stderr.String(), err.Error()))
	}
	return nil
}

// System speaks with the platform's own voice: say on macOS; spd-say, espeak-ng or espeak on Linux.
type System struct {
	LookPath func(string) (string, error)
	Run      Run
}

var systemVoices = map[string][][]string{
	"darwin": {{"say", "-f", "-"}},
	"linux":  {{"spd-say", "-w", "-e"}, {"espeak-ng", "--stdin"}, {"espeak", "--stdin"}},
}

func (s System) Speak(ctx context.Context, text string) error {
	look, r := s.LookPath, s.Run
	if look == nil {
		look = exec.LookPath
	}
	if r == nil {
		r = run
	}
	for _, c := range systemVoices[runtime.GOOS] {
		if p, err := look(c[0]); err == nil {
			return r(ctx, text, p, c[1:]...)
		}
	}
	return errors.New("no voice to speak with: install speech-dispatcher or espeak-ng")
}

// Piper is local neural speech (crit's piper.go): a voice is an .onnx file in Dir, and what it writes is played
// with afplay, paplay, pw-play or aplay.
type Piper struct {
	Dir      string // ~/.oriel/piper
	Voice    string // a voice's name in Dir; the first there when ""
	LookPath func(string) (string, error)
	Run      Run
}

// Voices are the voice models in Dir, by name.
func (p Piper) Voices() []string {
	m, _ := filepath.Glob(filepath.Join(p.Dir, "*.onnx"))
	out := make([]string, 0, len(m))
	for _, f := range m {
		out = append(out, strings.TrimSuffix(filepath.Base(f), ".onnx"))
	}
	return out
}

var players = [][]string{{"afplay"}, {"paplay"}, {"pw-play"}, {"aplay", "-q"}}

func (p Piper) Speak(ctx context.Context, text string) error {
	look, r := p.LookPath, p.Run
	if look == nil {
		look = exec.LookPath
	}
	if r == nil {
		r = run
	}
	bin, err := look("piper")
	if err != nil {
		return errors.New("piper isn't installed")
	}
	name := p.Voice
	if vs := p.Voices(); name == "" && len(vs) > 0 {
		name = vs[0]
	}
	model := filepath.Join(p.Dir, name+".onnx")
	if name == "" || strings.ContainsAny(name, `/\`) {
		return errors.New("no Piper voice in " + p.Dir)
	}
	if _, err := os.Stat(model); err != nil {
		return errors.New("no Piper voice at " + model)
	}
	tmp, err := os.CreateTemp("", "oriel-speak-*.wav")
	if err != nil {
		return err
	}
	out := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(out)
	if err := r(ctx, text, bin, "--model", model, "--output_file", out); err != nil {
		return err
	}
	for _, c := range players {
		if pl, err := look(c[0]); err == nil {
			return r(ctx, "", pl, append(c[1:], out)...)
		}
	}
	return errors.New("nothing to play the speech with: install pipewire, pulseaudio or alsa-utils")
}

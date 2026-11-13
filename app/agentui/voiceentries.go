package agentui

import (
	"context"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// VoiceEntries are dictation's from the start: its options and, while voice.dictation is on, a Dictator that puts
// the mic in every long field and loads Voice on its first Start, handing over to it.
type VoiceEntries struct {
	kit.Entries
	Voice *Voice
	h     plugin.Host
	stub  registry.Disposable
}

// NewVoiceEntries are v's entries.
func NewVoiceEntries(v *Voice) *VoiceEntries {
	return &VoiceEntries{Entries: kit.Entries{ID: "voice.entries", Options: voiceOptions()}, Voice: v}
}

func (x *VoiceEntries) Activate(ctx context.Context, h plugin.Host) error {
	x.h = h
	if err := x.Entries.Activate(ctx, h); err != nil {
		return err
	}
	x.Add(registry.DisposeFunc(h.Options().Watch("voice.dictation", func() { h.Do(x.settle) })))
	x.Add(registry.DisposeFunc(func() {
		if x.stub != nil {
			x.stub.Dispose()
		}
	}))
	x.settle()
	return nil
}

func (x *VoiceEntries) settle() {
	v, _, _ := x.h.Options().Get("voice.dictation", registry.At{})
	on, _ := v.(bool)
	switch {
	case on && x.stub == nil:
		x.stub = plugin.ProvideUI[widget.Dictator](x.h, voiceStub{x})
	case !on && x.stub != nil:
		x.stub.Dispose()
		x.stub = nil
	}
}

// voiceStub is the Dictator until Voice loads, which then provides its own over it.
type voiceStub struct{ x *VoiceEntries }

func (s voiceStub) Start(target ui.ID) error {
	if err := s.x.h.Run(context.Background(), registry.LoadCommand(s.x.Voice.Manifest().ID), nil); err != nil &&
		!registry.IsNoCommand(err, registry.LoadCommand(s.x.Voice.Manifest().ID)) {
		return err
	}
	return s.x.Voice.Start(target)
}

func (s voiceStub) Stop() {
	if s.x.Voice.h != nil {
		s.x.Voice.Stop()
	}
}

func (s voiceStub) State(target ui.ID) widget.DictationState {
	if s.x.Voice.h != nil {
		return s.x.Voice.State(target)
	}
	return widget.DictationState{}
}

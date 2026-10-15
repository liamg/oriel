---
id: agent/voice
covers: [voice.dictate, voice.cancel, voice.download, voice.speak, app.overlay.voice]
related: [agent/prompts, agent/console]
---

# Voice

Dictation into any text field, an optional wake word, and spoken replies. Audio comes from a recorder Oriel runs
(`pw-record`, `parecord`, `arecord`, sox's `rec` or `ffmpeg`, the first installed, or `voice.recorder`) and is turned
into text by whisper.cpp's `whisper-server`, which Oriel starts when it's first needed and stops when idle, or by an
OpenAI-compatible endpoint (`voice.url`).

## Dictation {#dictation}

`⌃⇧Space` (also `⌘⇧Space` on macOS) in a text field, or in the editor in insert mode, starts listening; a badge by
the field says **listening**, and turns red once it hears you. Words appear at the cursor as you speak: the phrase is
transcribed again about once a second and replaced in place, and a pause commits it and starts the next.
`⌃⇧Space` again stops, after a last pass at the phrase; `esc` stops and takes back the phrase being heard. Moving
focus away stops it too: dictation never types anywhere else.

The first time, Oriel offers to download the Whisper model (`voice.model`; `base.en`, or `small.en` on Apple
silicon), checked against its checksum, into `~/.oriel/whisper`. `voice.dictation` turns it off.

## The wake word {#wake}

With `voice.wake` on, Oriel listens for `voice.wake_word` ("computer" by default) said after a pause. What you say
after it goes to the [assistant](agent/assistant), whose answer is spoken (`assistant.wake`; it takes it through a
`VoiceWake` hook, as a plugin could). With that off, it goes to the agent of the session in front, and its reply is
spoken when it ends the turn; with no session in front, it opens the omnibox with it.

## Spoken replies {#speech}

`voice.speech`: `system` (macOS `say`, or `spd-say` or `espeak-ng` on Linux), `piper` (a voice in `~/.oriel/piper`,
`voice.piper_voice`), or `off`. `voice.speak {text}` says anything.

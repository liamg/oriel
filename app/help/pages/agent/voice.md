---
id: agent/voice
covers: [voice.download, voice.speak]
related: [agent/prompts, agent/console]
---

# Voice

Dictation into any long text field, and spoken replies. Oriel hears the microphone itself (or through
`voice.recorder`, a recorder program) and turns speech into text with the Whisper built into it, or an
OpenAI-compatible endpoint (`voice.url`).

## Dictation {#dictation}

Every long field (prompts, comments, replies, commit messages, PR bodies) has a small mic at its bottom right; it
hides while you type. Click it, or press `⌘⇧Space` (`Ctrl⇧Space` on Linux) in the field, and speak: words appear at
the cursor, muted while the phrase is still being heard and corrected in place. The border turns orange and moves
with your voice.

Any key stops it (and is typed), as do the mic or the shortcut again, a click elsewhere, or 4 s of quiet. `esc`
stops and drops the phrase still being heard.

The Whisper model (`voice.model`, `base.en` by default) downloads in the background when Oriel starts, into
`~/.oriel/whisper`, checked against its checksum. Until it's there, the mic says how far it has got.
`voice.dictation` turns the mics off.

## Spoken replies {#speech}

`voice.speech`: `system` (macOS `say`, or `spd-say` or `espeak-ng` on Linux), `piper` (a voice in `~/.oriel/piper`,
`voice.piper_voice`), or `off`. `voice.speak {text}` says anything.

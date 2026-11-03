package voice

import (
	"errors"
	"fmt"
	"os"
)

// State is whether dictation can transcribe yet.
type State int

const (
	Ready       State = iota
	Unavailable       // nothing to transcribe with: no whisper.cpp built in, no whisper-server, no voice.url
	NoModel           // the model isn't on disk and isn't downloading
	Downloading       // the model is on its way
	Failed            // the model's download failed; Prepare tries again
)

// Readiness is whether dictation can transcribe, and if not, why.
type Readiness struct {
	State    State
	Model    string
	Download DownloadStatus // while Downloading, and after it Failed
	Err      error          // what's wrong, unless Ready or Downloading
}

// Percent is how much of the model has arrived, or -1 when that isn't known.
func (r Readiness) Percent() int {
	if r.Download.Total <= 0 {
		return -1
	}
	return int(r.Download.Got * 100 / r.Download.Total)
}

// String is what to tell the user.
func (r Readiness) String() string {
	switch r.State {
	case Ready:
		return "Dictation is ready"
	case Downloading:
		if p := r.Percent(); p >= 0 {
			return fmt.Sprintf("Dictation is getting ready: downloading the Whisper model (%d%%)", p)
		}
		return "Dictation is getting ready: downloading the Whisper model"
	case Failed:
		return "The Whisper model's download failed: " + r.Download.Error
	}
	return r.Err.Error()
}

// Readiness says whether a transcription would run now.
func (w *Whisper) Readiness() Readiness {
	st := w.Settings()
	if st.URL != "" {
		return Readiness{State: Ready}
	}
	path, name := w.ModelPath(st.Model)
	r := Readiness{Model: name}
	if !w.Installed() {
		r.State, r.Err = Unavailable, ErrNoBinary
		return r
	}
	if _, err := os.Stat(path); err == nil {
		return r
	}
	d, ok := w.DownloadState(name)
	switch {
	case ok && !d.Done:
		r.State, r.Download = Downloading, d
	case ok && d.Error != "":
		r.State, r.Download, r.Err = Failed, d, errors.New(d.Error)
	default:
		r.State, r.Err = NoModel, &NoModelError{Name: name, Path: path}
	}
	return r
}

// Prepare is Readiness, first starting the model's download at full speed (or retrying a failed one, or speeding up
// the background one) when it's missing and known.
func (w *Whisper) Prepare() Readiness {
	r := w.Readiness()
	switch r.State {
	case NoModel, Failed, Downloading:
		if _, err := w.Download(r.Model); err != nil {
			if r.State == NoModel {
				r.Err = err
			}
			return r
		}
		return w.Readiness()
	}
	return r
}

package agent

// WebTooled is a provider whose agent reaches the web with tools of its own, which its gate asks about: what a
// caller allows to let the agent search the web.
type WebTooled interface {
	WebTools() []string // by the name a Permission has: WebSearch, WebFetch
}

// WebToolsOf are p's own web tools; none for a provider that has none that ask.
func WebToolsOf(p Provider) []string {
	if w, ok := p.(WebTooled); ok {
		return w.WebTools()
	}
	return nil
}

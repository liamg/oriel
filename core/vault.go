package core

import "slices"

// Sealed is an event holding text its log mustn't keep: Seal is the event as the log has it, and that text by key;
// Unseal puts the text back from get.
type Sealed interface {
	Seal() (Event, map[string]string)
	Unseal(get func(key string) (string, bool)) Event
}

// Vault keeps the text sealed events take out of their log, somewhere of its own that is never shared.
type Vault interface {
	Get(key string) (string, bool)
	// Put keeps texts by key.
	Put(texts map[string]string) error
}

// sealThreads are threads with each redacted comment's body taken out into texts.
func sealThreads(session SessionID, threads []Thread, texts map[string]string) []Thread {
	if !slices.ContainsFunc(threads, holdsRedacted) {
		return threads
	}
	out := slices.Clone(threads)
	for i, t := range out {
		if !holdsRedacted(t) {
			continue
		}
		out[i].Comments = slices.Clone(t.Comments)
		for j, c := range out[i].Comments {
			if c.Redacted && c.Body != "" {
				texts[commentKey(session, t.ID, c.ID)] = c.Body
				out[i].Comments[j].Body = ""
			}
		}
	}
	return out
}

func holdsRedacted(t Thread) bool {
	return slices.ContainsFunc(t.Comments, func(c Comment) bool { return c.Redacted && c.Body != "" })
}

// unsealThreads puts back the bodies sealThreads took out.
func unsealThreads(session SessionID, threads []Thread, get func(string) (string, bool)) {
	for i, t := range threads {
		for j, c := range t.Comments {
			if c.Redacted && c.Body == "" {
				if s, ok := get(commentKey(session, t.ID, c.ID)); ok {
					threads[i].Comments[j].Body = s
				}
			}
		}
	}
}

func commentKey(session SessionID, thread ThreadID, comment string) string {
	return string(session) + "/" + string(thread) + "/" + comment
}

func (e ThreadUpdated) Seal() (Event, map[string]string) {
	texts := map[string]string{}
	e.Thread = sealThreads(e.Session, []Thread{e.Thread}, texts)[0]
	return e, texts
}

func (e ThreadUpdated) Unseal(get func(string) (string, bool)) Event {
	ts := []Thread{e.Thread}
	unsealThreads(e.Session, ts, get)
	e.Thread = ts[0]
	return e
}

func (e SessionSynced) Seal() (Event, map[string]string) {
	texts := map[string]string{}
	e.Session.Threads = sealThreads(e.Session.ID, e.Session.Threads, texts)
	return e, texts
}

func (e SessionSynced) Unseal(get func(string) (string, bool)) Event {
	unsealThreads(e.Session.ID, e.Session.Threads, get)
	return e
}

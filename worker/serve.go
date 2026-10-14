package worker

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/wire"
	"github.com/liamg/oriel/worker/stats"
)

// Serve answers an app on conn until it goes away.
func (w *Worker) Serve(conn wire.Conn) {
	w.mu.Lock()
	w.apps = append(w.apps, conn)
	w.mu.Unlock()
	go func() {
		<-conn.Done()
		w.mu.Lock()
		w.apps = slices.DeleteFunc(w.apps, func(c wire.Conn) bool { return c == conn })
		w.mu.Unlock()
	}()
	fn := func(method string, h wire.Handler) { conn.Handle(method, h) }
	fn(MethodInfo, wire.Func(func(context.Context, struct{}) (Info, error) { return w.Info(), nil }))
	fn(MethodHandOver, wire.Func(func(context.Context, struct{}) (any, error) {
		go w.HandOver()
		return nil, nil
	}))
	fn(MethodSessions, wire.Func(func(context.Context, struct{}) ([]core.SessionState, error) { return w.store.State().Sessions, nil }))
	fn(MethodStart, wire.Func(func(ctx context.Context, s core.SessionSpec) (core.SessionID, error) { return w.Start(ctx, s) }))
	fn(MethodResume, wire.Func(func(ctx context.Context, r SessionRef) (any, error) { return nil, w.Resume(ctx, r.Session) }))
	fn(MethodStop, wire.Func(func(ctx context.Context, r SessionRef) (any, error) { return nil, w.Stop(ctx, r.Session) }))
	fn(MethodArchive, wire.Func(func(ctx context.Context, r SessionRef) (any, error) { return nil, w.Archive(ctx, r.Session) }))
	fn(MethodDelete, wire.Func(func(ctx context.Context, r SessionRef) (any, error) { return nil, w.Delete(ctx, r.Session) }))
	fn(MethodJobs, wire.Func(func(context.Context, struct{}) ([]Job, error) { return w.Jobs(), nil }))
	fn(MethodJobSave, wire.Func(func(_ context.Context, j Job) (Job, error) { return w.SaveJob(j) }))
	fn(MethodJobDelete, wire.Func(func(_ context.Context, r JobRef) (any, error) { return nil, w.DeleteJob(r.Name) }))
	fn(MethodJobRun, wire.Func(func(ctx context.Context, r JobRef) (core.SessionID, error) { return w.RunJob(ctx, r.Name) }))
	fn(MethodNextPR, wire.Func(func(ctx context.Context, r SessionRef) (any, error) { return nil, w.NextPR(ctx, r.Session) }))
	fn(MethodStartWork, wire.Func(func(ctx context.Context, r StartWorkReq) (any, error) { return nil, w.StartWork(ctx, r) }))
	fn(MethodAddRepo, wire.Func(func(ctx context.Context, r AddRepoReq) (any, error) {
		return nil, w.AddRepo(ctx, r.Session, core.RepoID(r.Repo))
	}))
	fn(MethodRename, wire.Func(func(_ context.Context, r RenameReq) (any, error) { return nil, w.Rename(r.Session, r.Name) }))
	fn(MethodSearch, wire.Func(func(_ context.Context, r SearchReq) ([]Hit, error) { return w.Search(r), nil }))
	fn(MethodComment, wire.Func(func(ctx context.Context, r CommentReq) (core.ThreadID, error) { return w.Comment(ctx, r) }))
	fn(MethodThread, wire.Func(func(_ context.Context, r ThreadReq) (any, error) { return nil, w.SetThread(r) }))
	fn(MethodAnswer, wire.Func(func(_ context.Context, r AnswerReq) (any, error) { return nil, w.Answer(r) }))
	fn(MethodSend, wire.Func(func(_ context.Context, r SendReq) (any, error) { return nil, w.Send(r) }))
	fn(MethodFork, wire.Func(func(_ context.Context, r ForkReq) (string, error) { return w.Fork(r.Session, r.Prompt) }))
	fn(MethodInterrupt, wire.Func(func(_ context.Context, r SessionRef) (any, error) { return nil, w.Interrupt(r.Session) }))
	fn(MethodTask, wire.Func(func(_ context.Context, r TaskReq) (any, error) { return nil, w.SetTask(r.Session, r.Task) }))
	fn(MethodRepos, wire.Func(func(context.Context, struct{}) ([]core.Repo, error) { return w.repos.all(), nil }))
	fn(MethodRepoAdd, wire.Func(func(ctx context.Context, path string) (core.Repo, error) { return w.repos.add(ctx, w.opts.ID, path) }))
	fn(MethodRepoClone, wire.Func(func(ctx context.Context, r CloneReq) (core.Repo, error) { return w.CloneRepo(ctx, r) }))
	fn(MethodTrustSettings, wire.Func(func(_ context.Context, path string) (any, error) { return nil, w.TrustSettings(path) }))
	fn(MethodProviders, wire.Func(func(ctx context.Context, _ struct{}) ([]ProviderInfo, error) { return w.ProviderInfo(ctx), nil }))
	fn(MethodKinds, wire.Func(func(context.Context, struct{}) ([]KindSpec, error) { return w.kinds.All(), nil }))
	fn(MethodLogAppend, wire.Func(func(_ context.Context, r LogAppendReq) (any, error) { return nil, w.appendLog(r) }))
	fn(MethodLogRead, wire.Func(func(_ context.Context, r SessionRef) (any, error) { return w.readLog(r.Session) }))
	fn(MethodRunTests, wire.Func(func(_ context.Context, r core.TestRunRecorded) (any, error) {
		if r.Run.ID == "" {
			r.Run.ID = newID(4)
		}
		return r.Run.ID, w.emit(r)
	}))
	fn(MethodSandboxConfig, wire.Func(func(_ context.Context, cfg sandbox.Config) (any, error) { return nil, w.SetSandboxConfig(cfg) }))
	fn(MethodStatsQuery, wire.Func(func(_ context.Context, q stats.Query) (stats.Report, error) { return w.StatsQuery(q), nil }))
	fn(MethodStatsSession, wire.Func(func(_ context.Context, r StatsSessionReq) (stats.Detail, error) { return w.SessionStats(r) }))
	fn(MethodUsageHistory, wire.Func(func(_ context.Context, r UsageHistoryReq) ([]core.ProviderUsage, error) {
		return w.UsageHistory(r), nil
	}))
	fn(MethodUsageRead, wire.Func(func(ctx context.Context, _ struct{}) (any, error) { return nil, w.ReadUsage(ctx) }))
	fn(MethodTermInput, wire.Func(func(_ context.Context, r TermReq) (any, error) { return nil, w.termInput(r) }))
	conn.HandleStream(MethodEvents, wire.StreamFunc(w.serveEvents))
	conn.HandleStream(MethodTerminal, wire.StreamFunc(w.serveTerminal))
}

// serveEvents sends every session as it is, then each event as it happens.
func (w *Worker) serveEvents(ctx context.Context, _ struct{}, send func(any) error) error {
	ch := make(chan core.Event, 1024)
	var dropped atomic.Bool
	st, cancel := w.store.SubscribeState(core.Filter{}, func(ev core.Event) {
		select {
		case ch <- ev:
		default:
			dropped.Store(true)
		}
	})
	defer cancel()
	out := func(ev core.Event) error {
		env, err := core.EncodeEvent(ev)
		if err != nil {
			return nil
		}
		return send(env)
	}
	if err := out(core.WorkerChanged{Worker: w.workerState()}); err != nil {
		return err
	}
	for _, u := range st.Usage {
		if err := out(core.UsageSampled{Usage: u}); err != nil {
			return err
		}
	}
	for _, se := range st.Sessions {
		if err := out(core.SessionSynced{Session: se}); err != nil {
			return err
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-w.closed:
			return nil
		case ev := <-ch:
			if dropped.Load() {
				return errors.New("the app fell behind: reconnect")
			}
			if err := out(ev); err != nil {
				return err
			}
		}
	}
}

// Answer answers an ask.
func (w *Worker) Answer(r AnswerReq) error {
	h, _ := w.hostFor(r.Session, false)
	if h == nil {
		return errors.New("nothing in that session is asking")
	}
	return h.answer(r)
}

// Send is a message to the agent outside any thread: sent now if it's free, queued for the turn's end if not.
func (w *Worker) Send(r SendReq) error {
	se, err := w.session(r.Session)
	if err != nil {
		return err
	}
	if se.Agent.Provider == "" {
		return errors.New("this session has no agent")
	}
	h, err := w.hostFor(r.Session, true)
	if err != nil {
		return err
	}
	return h.queue(r.Text, r.Urgent)
}

// Fork answers a side question from the session's agent, outside its conversation.
func (w *Worker) Fork(id core.SessionID, prompt string) (string, error) {
	h, _ := w.hostFor(id, false)
	if h == nil || !h.running() {
		return "", errors.New("the session's agent isn't running")
	}
	return h.fork(prompt)
}

// Interrupt stops the turn the agent is on.
func (w *Worker) Interrupt(id core.SessionID) error {
	h, _ := w.hostFor(id, false)
	if h == nil || !h.running() {
		return errors.New("the agent isn't running")
	}
	h.urgent()
	return nil
}

func (w *Worker) appendLog(r LogAppendReq) error {
	w.mu.Lock()
	l := w.open[r.Session]
	if l == nil {
		var err error
		if l, err = w.sessLogs.Open(r.Session); err != nil {
			w.mu.Unlock()
			return err
		}
		w.open[r.Session] = l
	}
	w.mu.Unlock()
	return l.Append(r.Record)
}

func (w *Worker) readLog(id core.SessionID) (any, error) {
	w.mu.Lock()
	l := w.open[id]
	if l == nil {
		var err error
		if l, err = w.sessLogs.Open(id); err != nil {
			w.mu.Unlock()
			return nil, err
		}
		w.open[id] = l
	}
	w.mu.Unlock()
	return l.Records()
}

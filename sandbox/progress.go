package sandbox

import "context"

// Progress is what a slow step of readying a sandbox is doing, for whoever waits on it.
type Progress struct {
	Step string // "Building the sandbox image"
	Note string // why it's slow, when that's worth saying: it happens once
}

type progressKey struct{}

// WithProgress is ctx with fn hearing each step reported under it.
func WithProgress(ctx context.Context, fn func(Progress)) context.Context {
	return context.WithValue(ctx, progressKey{}, fn)
}

// Report tells ctx's listener, if any, what a step is doing.
func Report(ctx context.Context, p Progress) {
	if fn, ok := ctx.Value(progressKey{}).(func(Progress)); ok {
		fn(p)
	}
}

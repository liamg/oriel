package update

import "io"

// The stages of an update, in order.
const (
	StageDownload = "downloading"
	StageVerify   = "verifying"
	StageExtract  = "extracting"
	StageCheck    = "checking"
	StageSwap     = "swapping"
)

// Step is how far an update has got: its stage, the bytes of a download so far, and what it's working on.
type Step struct {
	Stage       string
	Done, Total int64
	Detail      string
}

// Progress is told of each step; nil is told nothing.
type Progress func(Step)

func (p Progress) report(s Step) {
	if p != nil {
		p(s)
	}
}

// counting reports a download's bytes as they're read.
type counting struct {
	r     io.Reader
	n     int64
	total int64
	name  string
	p     Progress
}

func (c *counting) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	if n > 0 {
		c.n += int64(n)
		c.p.report(Step{Stage: StageDownload, Done: c.n, Total: c.total, Detail: c.name})
	}
	return n, err
}

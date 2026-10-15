package core

import (
	"fmt"
	"slices"
	"time"
)

func init() {
	RegisterEvent[Verified]("Verified")
}

// A verify quiz's results (agents.md, "Verify & raise"): every answer right or partly right, at least one wrong, or
// skipped by the user.
const (
	VerifyPassed  = "passed"
	VerifyFailed  = "failed"
	VerifySkipped = "skipped"
)

// The verdicts on an answer.
const (
	VerdictRight  = "right"
	VerdictPartly = "partly"
	VerdictWrong  = "wrong"
)

// Verification is a verify quiz taken before raising a session's PRs: its questions, your answers and their
// verdicts.
type Verification struct {
	Result    string         `json:"result"`
	Projects  []string       `json:"projects,omitempty"`
	By        string         `json:"by,omitempty"` // who asked: "claude · fork, no tools"
	Questions []QuizQuestion `json:"questions,omitempty"`
	At        time.Time      `json:"at,omitzero"`
}

// QuizQuestion is one question of a quiz, and how it went; File and Line are where to read, for a partial or wrong
// answer.
type QuizQuestion struct {
	Question string `json:"question"`
	Answer   string `json:"answer,omitempty"`
	Verdict  string `json:"verdict,omitempty"`
	Note     string `json:"note,omitempty"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
}

// Count is how many answers got verdict.
func (v Verification) Count(verdict string) int {
	n := 0
	for _, q := range v.Questions {
		if q.Verdict == verdict {
			n++
		}
	}
	return n
}

// Verified records a verify quiz's result in the session, in its log.
type Verified struct {
	Session      SessionID `json:"session"`
	Verification `json:"verification"`
}

func (e Verified) Scope() SessionID { return e.Session }

func (e Verified) Apply(s *State) error {
	switch e.Result {
	case VerifyPassed, VerifyFailed, VerifySkipped:
	default:
		return fmt.Errorf("a verification is passed, failed or skipped, not %q", e.Result)
	}
	se, err := s.mustSession(e.Session)
	if err != nil {
		return err
	}
	se.Verifications = append(slices.Clone(se.Verifications), e.Verification)
	return nil
}

// Verification is the session's last verify quiz, if it has taken one.
func (s SessionState) Verification() (Verification, bool) {
	if len(s.Verifications) == 0 {
		return Verification{}, false
	}
	return s.Verifications[len(s.Verifications)-1], true
}

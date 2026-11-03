package transcript

import (
	"encoding/json"
	"strings"

	"github.com/liamg/oriel/core"
)

// AskTool is the tool an agent asks the user questions with.
const AskTool = "AskUserQuestion"

// Questions reads the questions of an AskUserQuestion input, dropping any without text.
func Questions(input json.RawMessage) []core.Question {
	var in struct {
		Questions []struct {
			Header   string `json:"header"`
			Question string `json:"question"`
			Multi    bool   `json:"multiSelect"`
			Options  []struct {
				Label string `json:"label"`
			} `json:"options"`
		} `json:"questions"`
	}
	if json.Unmarshal(input, &in) != nil {
		return nil
	}
	var out []core.Question
	for _, q := range in.Questions {
		if strings.TrimSpace(q.Question) == "" {
			continue
		}
		c := core.Question{Header: q.Header, Question: q.Question, Multi: q.Multi}
		for _, o := range q.Options {
			c.Options = append(c.Options, o.Label)
		}
		out = append(out, c)
	}
	return out
}

package cloudusage

import (
	"bytes"
	"encoding/json"
)

// Observer retains at most one SSE line or one bounded JSON response in memory.
// Only numeric usage is exposed; text is never included in a receipt.
type Observer struct {
	Streaming bool
	Embedding bool
	Input     int
	Output    int
	Measured  bool
	pending   []byte
	dropping  bool
}

const maxObservationBytes = 1 << 20

func (o *Observer) Write(p []byte) (int, error) {
	n := len(p)
	if !o.Streaming {
		if len(o.pending)+len(p) > maxObservationBytes {
			o.pending, o.dropping = nil, true
		}
		if !o.dropping {
			o.pending = append(o.pending, p...)
		}
		return n, nil
	}
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		part := p
		if i >= 0 {
			part = p[:i]
		}
		if len(o.pending)+len(part) > maxObservationBytes {
			o.pending, o.dropping = nil, true
		}
		if !o.dropping {
			o.pending = append(o.pending, part...)
		}
		if i < 0 {
			break
		}
		if !o.dropping {
			line := bytes.TrimSpace(o.pending)
			if bytes.HasPrefix(line, []byte("data:")) {
				o.observe(bytes.TrimSpace(line[5:]))
			}
		}
		o.pending, o.dropping = o.pending[:0], false
		p = p[i+1:]
	}
	return n, nil
}

func (o *Observer) Finish() {
	if !o.Streaming && !o.dropping {
		o.observe(o.pending)
	}
	o.pending = nil
}

func (o *Observer) observe(body []byte) {
	var parsed struct {
		Usage *struct {
			PromptTokens     *int `json:"prompt_tokens"`
			CompletionTokens *int `json:"completion_tokens"`
			InputTokens      *int `json:"input_tokens"`
			OutputTokens     *int `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &parsed) != nil || parsed.Usage == nil {
		return
	}
	u := parsed.Usage
	input, output := u.PromptTokens, u.CompletionTokens
	if input == nil {
		input = u.InputTokens
	}
	if output == nil {
		output = u.OutputTokens
	}
	if o.Embedding && output == nil {
		zero := 0
		output = &zero
	}
	if input != nil && output != nil && *input >= 0 && *output >= 0 {
		o.Input, o.Output, o.Measured = *input, *output, true
	}
}

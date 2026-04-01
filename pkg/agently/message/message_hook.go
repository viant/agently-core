package message

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
)

func (m *MessageView) OnFetch(ctx context.Context) error {
	if len(m.ElicitationBody) == 0 {
		return nil
	}
	body := m.ElicitationBody
	if m.ElicitationCompression == "gzip" {
		gr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil // non-fatal: leave Elicitation nil
		}
		var buf bytes.Buffer
		if _, err = buf.ReadFrom(gr); err != nil {
			return nil
		}
		_ = gr.Close()
		body = buf.Bytes()
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil // non-fatal: malformed payload should not break the fetch
	}
	m.Elicitation = out
	return nil
}

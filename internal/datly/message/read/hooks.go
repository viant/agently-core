package read

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
)

func (m *MessageView) OnFetch(ctx context.Context) error {
	if m.ReadMode == "transcript" || len(m.ElicitationBody) == 0 {
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

func (input *MessagesInput) Init(context.Context) error {
	switch input.ReadMode {
	case "rows":
		return nil
	case "byId", "transcript":
		if input.Id == "" {
			return fmt.Errorf("message lookup requires id")
		}
	case "elicitation":
		if input.ConversationId == "" || input.ElicitationId == "" {
			return fmt.Errorf("elicitation lookup requires conversation and elicitation")
		}
		input.SetLimit(1)
	case "linkedElicitation":
		if input.LinkedConversationId == "" || input.ElicitationId == "" {
			return fmt.Errorf("linked elicitation lookup requires link and elicitation")
		}
	case "parentElicitation":
		if input.ParentMessageId == "" || input.ElicitationId == "" {
			return fmt.Errorf("parent elicitation lookup requires parent and elicitation")
		}
	default:
		return fmt.Errorf("unsupported message read mode")
	}
	return nil
}

package agent

import (
	"context"
	"testing"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
)

func TestService_buildHistory_includesSteerTaskMessage(t *testing.T) {
	now := time.Now()
	turnID := "turn-1"
	transcript := apiconv.Transcript{
		{
			Id:     turnID,
			Status: "running",
			Message: []*agconv.MessageView{
				(*agconv.MessageView)(&apiconv.Message{
					Id:        "user-1",
					Role:      "user",
					Type:      "text",
					Content:   stringPtr("Analyze repo."),
					CreatedAt: now,
				}),
				(*agconv.MessageView)(&apiconv.Message{
					Id:        "assistant-1",
					Role:      "assistant",
					Type:      "text",
					Content:   stringPtr("I will inspect the repository."),
					CreatedAt: now.Add(time.Second),
				}),
				(*agconv.MessageView)(&apiconv.Message{
					Id:        "steer-1",
					Role:      "user",
					Type:      "task",
					Content:   stringPtr("Focus on build failures only."),
					CreatedAt: now.Add(2 * time.Second),
				}),
			},
		},
	}
	svc := &Service{}
	history, err := svc.buildHistory(context.Background(), transcript)
	if err != nil {
		t.Fatalf("buildHistory error: %v", err)
	}
	if got := len(history.Messages); got != 3 {
		t.Fatalf("expected 3 history messages, got %d", got)
	}
	last := history.Messages[len(history.Messages)-1]
	if last.Role != "user" {
		t.Fatalf("expected final history role user, got %q", last.Role)
	}
	if last.Content != "Focus on build failures only." {
		t.Fatalf("expected steer content in history, got %q", last.Content)
	}
}

func TestService_buildHistory_includesAsyncWaitSystemMessage(t *testing.T) {
	now := time.Now()
	turnID := "turn-1"
	asyncMode := asyncMessageMode
	transcript := apiconv.Transcript{
		{
			Id:     turnID,
			Status: "running",
			Message: []*agconv.MessageView{
				(*agconv.MessageView)(&apiconv.Message{
					Id:        "user-1",
					Role:      "user",
					Type:      "text",
					Content:   stringPtr("forecast deal 142133"),
					CreatedAt: now,
				}),
				(*agconv.MessageView)(&apiconv.Message{
					Id:        "async-1",
					Role:      "system",
					Type:      "text",
					Mode:      &asyncMode,
					Content:   stringPtr("Forecasting job is WAITING. Reuse the same request args."),
					CreatedAt: now.Add(time.Second),
				}),
			},
		},
	}
	svc := &Service{}
	history, err := svc.buildHistory(context.Background(), transcript)
	if err != nil {
		t.Fatalf("buildHistory error: %v", err)
	}
	if got := len(history.Messages); got != 2 {
		t.Fatalf("expected 2 history messages, got %d", got)
	}
	last := history.Messages[len(history.Messages)-1]
	if last.Role != "system" {
		t.Fatalf("expected async wait role system, got %q", last.Role)
	}
	if last.Content != "Forecasting job is WAITING. Reuse the same request args." {
		t.Fatalf("expected async wait content in history, got %q", last.Content)
	}
}

func stringPtr(value string) *string {
	return &value
}

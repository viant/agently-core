package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientSendMessageStream_ParsesStatusAndArtifactEvents(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/message:stream", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("response writer missing flusher")
		}
		w.Header().Set("Content-Type", "text/event-stream")

		writeEvent := func(payload interface{}) {
			result, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal result: %v", err)
			}
			envelope, err := json.Marshal(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      1,
				Result:  result,
			})
			if err != nil {
				t.Fatalf("marshal envelope: %v", err)
			}
			_, _ = fmt.Fprintf(w, "data:%s\n\n", envelope)
			flusher.Flush()
		}

		taskID := "task-123"
		ctxID := "conv-456"
		writeEvent(NewStatusEvent(&Task{
			ID:        taskID,
			ContextID: ctxID,
			Status: TaskStatus{
				State:     TaskStateRunning,
				UpdatedAt: time.Now().UTC(),
			},
		}, false))
		writeEvent(NewArtifactEvent(&Task{
			ID:        taskID,
			ContextID: ctxID,
		}, Artifact{
			ID:        "art-1",
			CreatedAt: time.Now().UTC(),
			Parts:     []Part{{Type: "text", Text: "guardian result"}},
		}, true, true))
		writeEvent(NewStatusEvent(&Task{
			ID:        taskID,
			ContextID: ctxID,
			Status: TaskStatus{
				State:     TaskStateCompleted,
				UpdatedAt: time.Now().UTC(),
			},
		}, true))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClient(server.URL + "/v1/message:send")
	task, err := client.SendMessage(context.Background(), []Message{{
		Role:  RoleUser,
		Parts: []Part{{Type: "text", Text: "hello"}},
	}}, nil)
	if err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if task == nil {
		t.Fatalf("SendMessage() returned nil task")
	}
	if task.ID != "task-123" {
		t.Fatalf("task.ID = %q, want %q", task.ID, "task-123")
	}
	if task.ContextID != "conv-456" {
		t.Fatalf("task.ContextID = %q, want %q", task.ContextID, "conv-456")
	}
	if task.Status.State != TaskStateCompleted {
		t.Fatalf("task.Status.State = %q, want %q", task.Status.State, TaskStateCompleted)
	}
	if len(task.Artifacts) != 1 {
		t.Fatalf("len(task.Artifacts) = %d, want 1", len(task.Artifacts))
	}
	if got := task.Artifacts[0].Parts[0].Text; got != "guardian result" {
		t.Fatalf("artifact text = %q, want %q", got, "guardian result")
	}
}

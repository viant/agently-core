package sdk

import (
	"testing"

	convstore "github.com/viant/agently-core/app/store/conversation"
)

func TestBuildCanonicalStatePreservesTurnErrorWithoutExecutionPages(t *testing.T) {
	errorMessage := "failed to stream: failed to start Stream: API key is required"
	state := BuildCanonicalState("conv-1", convstore.Transcript{{
		Id:           "turn-1",
		Status:       "failed",
		ErrorMessage: &errorMessage,
	}})

	if state == nil || len(state.Turns) != 1 {
		t.Fatalf("unexpected state: %#v", state)
	}
	if got := state.Turns[0].ErrorMessage; got != errorMessage {
		t.Fatalf("errorMessage = %q, want %q", got, errorMessage)
	}
}

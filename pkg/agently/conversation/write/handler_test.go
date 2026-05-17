package write

import "testing"

func TestMergeConversationPatch_PreservesUnpatchedMetadata(t *testing.T) {
	currentMetadata := `{"workspace":{"windowId":"order_1","presentation":"hosted","region":"chat.top"}}`
	currentStatus := "running"
	current := &Conversation{
		Id:       "conv-1",
		Metadata: &currentMetadata,
		Status:   &currentStatus,
		Has:      &ConversationHas{},
	}
	nextStatus := "succeeded"
	patch := &Conversation{
		Id:     "conv-1",
		Status: &nextStatus,
		Has: &ConversationHas{
			Id:     true,
			Status: true,
		},
	}

	merged := mergeConversationPatch(current, patch)
	if merged == nil {
		t.Fatalf("expected merged conversation")
	}
	if merged.Metadata == nil || *merged.Metadata != currentMetadata {
		t.Fatalf("expected metadata to be preserved, got %#v", merged.Metadata)
	}
	if merged.Status == nil || *merged.Status != nextStatus {
		t.Fatalf("expected status to be patched, got %#v", merged.Status)
	}
}

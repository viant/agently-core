package registry

import "testing"

func TestSnapshotBelongsToConversation_UsesWindowConversationID(t *testing.T) {
	snapshot := &Snapshot{
		Windows: []WindowSnapshot{
			{
				WindowID:       "reportBuilder__conv-new",
				WindowKey:      "reportBuilder",
				ConversationID: "conv-new",
				Presentation:   "hosted",
				Region:         "chat.top",
				ParentKey:      "chat/new",
			},
		},
	}

	if !snapshotBelongsToConversation(snapshot, "conv-new") {
		t.Fatalf("expected window-level conversation id to own snapshot")
	}
}

func TestSnapshotBelongsToConversation_DoesNotMatchChatOnly(t *testing.T) {
	snapshot := &Snapshot{
		Windows: []WindowSnapshot{
			{
				WindowID:  "chat/new",
				WindowKey: "chat/new",
			},
		},
	}

	if snapshotBelongsToConversation(snapshot, "conv-new") {
		t.Fatalf("expected chat-only snapshot not to match arbitrary conversation")
	}
}

func TestFilterSnapshotForConversation_FiltersHostedWindowsByConversation(t *testing.T) {
	snapshot := &Snapshot{
		ConversationID: "conv-new",
		Selected:       SnapshotSelected{WindowID: "reportBuilder__conv-old"},
		Windows: []WindowSnapshot{
			{
				WindowID:    "chat/new",
				WindowKey:   "chat/new",
				WindowTitle: "Chat",
			},
			{
				WindowID:       "reportBuilder__conv-old",
				WindowKey:      "reportBuilder",
				WindowTitle:    "Performance Metrics",
				ConversationID: "conv-old",
				Presentation:   "hosted",
				Region:         "chat.top",
				ParentKey:      "chat/new",
				InTab:          true,
			},
			{
				WindowID:       "reportBuilder__conv-new",
				WindowKey:      "reportBuilder",
				WindowTitle:    "Performance Metrics",
				ConversationID: "conv-new",
				Presentation:   "hosted",
				Region:         "chat.top",
				ParentKey:      "chat/new",
				InTab:          true,
			},
		},
	}

	got := filterSnapshotForConversation(snapshot, "conv-new")
	if got == nil {
		t.Fatalf("expected filtered snapshot")
	}
	if len(got.Windows) != 2 {
		t.Fatalf("expected chat + current hosted window only, got %#v", got.Windows)
	}
	if got.Windows[1].WindowID != "reportBuilder__conv-new" {
		t.Fatalf("expected current conversation hosted window, got %#v", got.Windows[1])
	}
	if got.Selected.WindowID != "" {
		t.Fatalf("expected stale selected hosted window to be cleared, got %#v", got.Selected.WindowID)
	}
}

func TestFilterSnapshotForConversation_KeepsNonHostedWindowsVisible(t *testing.T) {
	snapshot := &Snapshot{
		ConversationID: "conv-new",
		Windows: []WindowSnapshot{
			{
				WindowID:    "chat/new",
				WindowKey:   "chat/new",
				WindowTitle: "Chat",
			},
			{
				WindowID:     "schedule",
				WindowKey:    "schedule",
				WindowTitle:  "Automation",
				Presentation: "",
			},
		},
	}

	got := filterSnapshotForConversation(snapshot, "conv-new")
	if got == nil {
		t.Fatalf("expected filtered snapshot")
	}
	if len(got.Windows) != 2 {
		t.Fatalf("expected non-hosted windows to stay visible, got %#v", got.Windows)
	}
}

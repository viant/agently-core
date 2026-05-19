package llm

import "testing"

func TestApprovalConfig_EffectiveQueueBehavior_DefaultsToDetach(t *testing.T) {
	cfg := &ApprovalConfig{Mode: ApprovalModeQueue}
	if got := cfg.EffectiveQueueBehavior(); got != ApprovalQueueBehaviorDetach {
		t.Fatalf("expected default queue behavior %q, got %q", ApprovalQueueBehaviorDetach, got)
	}
	if !cfg.QueueDetaches() {
		t.Fatalf("expected queue mode without explicit behavior to detach by default")
	}
}

func TestApprovalConfig_EffectiveQueueBehavior_RespectsWait(t *testing.T) {
	cfg := &ApprovalConfig{Mode: ApprovalModeQueue, QueueBehavior: ApprovalQueueBehaviorWait}
	if got := cfg.EffectiveQueueBehavior(); got != ApprovalQueueBehaviorWait {
		t.Fatalf("expected explicit queue behavior %q, got %q", ApprovalQueueBehaviorWait, got)
	}
	if cfg.QueueDetaches() {
		t.Fatalf("expected explicit wait queue behavior to keep wait semantics")
	}
}

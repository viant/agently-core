package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/viant/agently-core/protocol/binding"
	goalsys "github.com/viant/agently-core/service/goal"
)

type fakeActiveGoalReader struct {
	goal *goalsys.Goal
}

func (f fakeActiveGoalReader) Current(context.Context, string) (*goalsys.Goal, error) {
	return f.goal, nil
}

func TestAppendActiveGoalSystemDocumentFromReader(t *testing.T) {
	b := &binding.Binding{}
	if !appendActiveGoalSystemDocumentFromReader(context.Background(), "conv-1", b, fakeActiveGoalReader{
		goal: &goalsys.Goal{
			ID:        "goal-1",
			Objective: "Create main.go that computes 2 + 3 and prints 5",
			Status:    goalsys.StatusActive,
		},
	}) {
		t.Fatalf("expected active goal append to report true")
	}

	if len(b.SystemDocuments.Items) != 1 {
		t.Fatalf("expected one active goal document, got %#v", b.SystemDocuments.Items)
	}
	doc := b.SystemDocuments.Items[0]
	if doc == nil {
		t.Fatalf("expected active goal document")
	}
	if doc.SourceURI != "goal://goal-1" {
		t.Fatalf("expected goal source uri, got %q", doc.SourceURI)
	}
	if doc.Metadata["kind"] != "active_goal" {
		t.Fatalf("expected active_goal metadata, got %#v", doc.Metadata)
	}
	if doc.Metadata["status"] != string(goalsys.StatusActive) {
		t.Fatalf("expected active status metadata, got %#v", doc.Metadata)
	}
	if !containsAll(doc.PageContent,
		"Active conversation goal:",
		"Create main.go that computes 2 + 3 and prints 5",
		"Stay focused on it across turns.",
		"redirect back to the goal",
	) {
		t.Fatalf("expected active goal guidance in page content, got %q", doc.PageContent)
	}
}

func TestAppendActiveGoalSystemDocumentFromReader_SkipsInactiveGoals(t *testing.T) {
	b := &binding.Binding{}
	if appendActiveGoalSystemDocumentFromReader(context.Background(), "conv-1", b, fakeActiveGoalReader{
		goal: &goalsys.Goal{
			ID:        "goal-2",
			Objective: "Do not inject me",
			Status:    goalsys.StatusPaused,
		},
	}) {
		t.Fatalf("expected paused goal append to report false")
	}
	if len(b.SystemDocuments.Items) != 0 {
		t.Fatalf("expected no document for paused goal, got %#v", b.SystemDocuments.Items)
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			return false
		}
	}
	return true
}

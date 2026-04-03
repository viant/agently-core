package sdk

import (
	"testing"
	"time"

	agconv "github.com/viant/agently-core/pkg/agently/conversation"
)

func TestLessTimeAndID(t *testing.T) {
	leftAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rightAt := leftAt.Add(time.Second)
	if !lessTimeAndID(leftAt, "a", rightAt, "b") {
		t.Fatalf("expected earlier time to sort first")
	}
	if !lessTimeAndID(leftAt, "a", leftAt, "b") {
		t.Fatalf("expected lower id to sort first on equal times")
	}
}

func TestLessToolMessage(t *testing.T) {
	firstSeq := 1
	secondSeq := 2
	first := &agconv.ToolMessageView{
		Id:        "a",
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Sequence:  &firstSeq,
	}
	second := &agconv.ToolMessageView{
		Id:        "b",
		CreatedAt: first.CreatedAt,
		Sequence:  &secondSeq,
	}
	if !lessToolMessage(first, second) {
		t.Fatalf("expected lower sequence to sort first")
	}
}

func TestLessPendingElicitation(t *testing.T) {
	left := &PendingElicitation{
		ElicitationID: "a",
		CreatedAt:     time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC),
	}
	right := &PendingElicitation{
		ElicitationID: "b",
		CreatedAt:     left.CreatedAt,
	}
	if !lessPendingElicitation(left, right) {
		t.Fatalf("expected lower elicitation id to sort first on equal times")
	}
}

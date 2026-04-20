package narrator

import (
	"context"
	"testing"

	asynccfg "github.com/viant/agently-core/protocol/async"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

func TestStartPreamble(t *testing.T) {
	rec := &asynccfg.OperationRecord{
		OperationIntent:  "inspect repo",
		OperationSummary: "workdir=/tmp/ws | orderId=2639076",
		Message:          "phase 1",
		Status:           "running",
		ToolName:         "tool:start",
	}
	got, err := StartPreamble(context.Background(), nil, rec)
	if err != nil {
		t.Fatalf("StartPreamble() error = %v", err)
	}
	if got != "inspect repo: phase 1" {
		t.Fatalf("StartPreamble() = %q", got)
	}
	got, err = StartPreamble(context.Background(), &asynccfg.Config{Narration: "none"}, rec)
	if err != nil {
		t.Fatalf("StartPreamble(none) error = %v", err)
	}
	if got != "" {
		t.Fatalf("StartPreamble(none) = %q", got)
	}
	got, err = StartPreamble(context.Background(), &asynccfg.Config{
		Narration:         "template",
		NarrationTemplate: "{{tool}} {{status}} {{intent}} {{summary}} {{message}}",
	}, rec)
	if err != nil {
		t.Fatalf("StartPreamble(template) error = %v", err)
	}
	if got != "tool:start running inspect repo workdir=/tmp/ws | orderId=2639076 phase 1" {
		t.Fatalf("StartPreamble(template) = %q", got)
	}
	ctx := runtimerequestctx.WithUserAsk(context.Background(), "summarize progress")
	got, err = StartPreamble(ctx, nil, rec)
	if err != nil {
		t.Fatalf("StartPreamble(user ask) error = %v", err)
	}
	if got != "summarize progress: phase 1" {
		t.Fatalf("StartPreamble(user ask) = %q", got)
	}
	ctx = WithLLMRunner(context.Background(), func(_ context.Context, in LLMInput) (string, error) {
		return in.UserAsk + "|" + in.Intent + "|" + in.Summary + "|" + in.Message, nil
	})
	got, err = StartPreamble(ctx, &asynccfg.Config{Narration: "llm"}, rec)
	if err != nil {
		t.Fatalf("StartPreamble(llm) error = %v", err)
	}
	if got != "inspect repo|workdir=/tmp/ws | orderId=2639076|phase 1" && got != "|inspect repo|workdir=/tmp/ws | orderId=2639076|phase 1" {
		t.Fatalf("StartPreamble(llm) = %q", got)
	}
	_, err = StartPreamble(context.Background(), &asynccfg.Config{Narration: "llm"}, rec)
	if err == nil {
		t.Fatal("expected llm mode without runner to error")
	}
}

func TestUpdatePreamble(t *testing.T) {
	ev := asynccfg.ChangeEvent{
		Intent:   "inspect repo",
		Summary:  "workdir=/tmp/ws | orderId=2639076",
		Message:  "phase 2",
		Status:   "running",
		ToolName: "tool:start",
	}
	got, err := UpdatePreamble(context.Background(), nil, ev)
	if err != nil {
		t.Fatalf("UpdatePreamble() error = %v", err)
	}
	if got != "inspect repo: phase 2" {
		t.Fatalf("UpdatePreamble() = %q", got)
	}
	got, err = UpdatePreamble(context.Background(), &asynccfg.Config{
		Narration:         "template",
		NarrationTemplate: "{{intent}} {{summary}} -> {{message}}",
	}, ev)
	if err != nil {
		t.Fatalf("UpdatePreamble(template) error = %v", err)
	}
	if got != "inspect repo workdir=/tmp/ws | orderId=2639076 -> phase 2" {
		t.Fatalf("UpdatePreamble(template) = %q", got)
	}
	ctx := runtimerequestctx.WithUserAsk(context.Background(), "summarize progress")
	got, err = UpdatePreamble(ctx, nil, ev)
	if err != nil {
		t.Fatalf("UpdatePreamble(user ask) error = %v", err)
	}
	if got != "summarize progress: phase 2" {
		t.Fatalf("UpdatePreamble(user ask) = %q", got)
	}
	ctx = WithLLMRunner(context.Background(), func(_ context.Context, in LLMInput) (string, error) {
		return in.UserAsk + "|" + in.Intent + "|" + in.Summary + "|" + in.Message, nil
	})
	got, err = UpdatePreamble(ctx, &asynccfg.Config{Narration: "llm"}, ev)
	if err != nil {
		t.Fatalf("UpdatePreamble(llm) error = %v", err)
	}
	if got != "|inspect repo|workdir=/tmp/ws | orderId=2639076|phase 2" {
		t.Fatalf("UpdatePreamble(llm) = %q", got)
	}
	_, err = UpdatePreamble(context.Background(), &asynccfg.Config{Narration: "llm"}, ev)
	if err == nil {
		t.Fatal("expected llm mode without runner to error")
	}
}

func TestFallbackUsesSummaryWhenIntentMissing(t *testing.T) {
	rec := &asynccfg.OperationRecord{
		OperationSummary: "workdir=/tmp/ws | orderId=2639076",
		Message:          "phase 1",
		Status:           "running",
		ToolName:         "tool:start",
	}
	got, err := StartPreamble(context.Background(), nil, rec)
	if err != nil {
		t.Fatalf("StartPreamble(summary fallback) error = %v", err)
	}
	if got != "workdir=/tmp/ws | orderId=2639076: phase 1" {
		t.Fatalf("StartPreamble(summary fallback) = %q", got)
	}
}

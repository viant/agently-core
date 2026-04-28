package narrator

import (
	"context"
	"os"
	"testing"
	"time"

	asynccfg "github.com/viant/agently-core/protocol/async"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

// TestMain seeds a small LLM timeout for tests only. The production
// default lives in the workspace baseline (`default.async.narrator.
// llmTimeout`); with that removed from this package, tests must pick a
// bound explicitly or risk unbounded runner contexts.
func TestMain(m *testing.M) {
	SetLLMTimeout(500 * time.Millisecond)
	os.Exit(m.Run())
}

func TestStartNarration(t *testing.T) {
	rec := &asynccfg.OperationRecord{
		OperationIntent:  "inspect repo",
		OperationSummary: "workdir=/tmp/ws | orderId=2639076",
		Message:          "phase 1",
		Status:           "running",
		ToolName:         "tool:start",
	}
	got, err := StartNarration(context.Background(), nil, rec)
	if err != nil {
		t.Fatalf("StartNarration() error = %v", err)
	}
	if got != "inspect repo: phase 1" {
		t.Fatalf("StartNarration() = %q", got)
	}
	got, err = StartNarration(context.Background(), &asynccfg.Config{Narration: "none"}, rec)
	if err != nil {
		t.Fatalf("StartNarration(none) error = %v", err)
	}
	if got != "" {
		t.Fatalf("StartNarration(none) = %q", got)
	}
	got, err = StartNarration(context.Background(), &asynccfg.Config{
		Narration:         "template",
		NarrationTemplate: "{{tool}} {{status}} {{intent}} {{summary}} {{message}}",
	}, rec)
	if err != nil {
		t.Fatalf("StartNarration(template) error = %v", err)
	}
	if got != "tool:start running inspect repo workdir=/tmp/ws | orderId=2639076 phase 1" {
		t.Fatalf("StartNarration(template) = %q", got)
	}
	ctx := runtimerequestctx.WithUserAsk(context.Background(), "summarize progress")
	got, err = StartNarration(ctx, nil, rec)
	if err != nil {
		t.Fatalf("StartNarration(user ask) error = %v", err)
	}
	if got != "summarize progress: phase 1" {
		t.Fatalf("StartNarration(user ask) = %q", got)
	}
	ctx = WithLLMRunner(context.Background(), func(_ context.Context, in LLMInput) (string, error) {
		return in.UserAsk + "|" + in.Intent + "|" + in.Summary + "|" + in.Message, nil
	})
	got, err = StartNarration(ctx, &asynccfg.Config{Narration: "llm"}, rec)
	if err != nil {
		t.Fatalf("StartNarration(llm) error = %v", err)
	}
	if got != "inspect repo|workdir=/tmp/ws | orderId=2639076|phase 1" && got != "|inspect repo|workdir=/tmp/ws | orderId=2639076|phase 1" {
		t.Fatalf("StartNarration(llm) = %q", got)
	}
	_, err = StartNarration(context.Background(), &asynccfg.Config{Narration: "llm"}, rec)
	if err == nil {
		t.Fatal("expected llm mode without runner to error")
	}
	got, err = StartNarration(context.Background(), &asynccfg.Config{Narration: "keydata"}, &asynccfg.OperationRecord{
		KeyData: []byte("Primary baseline read: delivery is constrained by targeting and supply.\n```csv\nx\n```"),
		Message: "fallback message",
	})
	if err != nil {
		t.Fatalf("StartNarration(keydata) error = %v", err)
	}
	if got != "Primary baseline read: delivery is constrained by targeting and supply." {
		t.Fatalf("StartNarration(keydata) = %q", got)
	}
	got, err = StartNarration(runtimerequestctx.WithUserAsk(context.Background(), "Troubleshoot 2654884"), &asynccfg.Config{Narration: "keydata"}, &asynccfg.OperationRecord{
		Message: "Checking whether the last-7-day delivery pattern confirms a setup or supply restriction rather than a pacing problem for ad order 2654884.",
	})
	if err != nil {
		t.Fatalf("StartNarration(keydata direct message) error = %v", err)
	}
	if got != "Checking whether the last-7-day delivery pattern confirms a setup or supply restriction rather than a pacing problem for ad order 2654884." {
		t.Fatalf("StartNarration(keydata direct message) = %q", got)
	}
}

func TestUpdateNarration(t *testing.T) {
	ev := asynccfg.ChangeEvent{
		Intent:   "inspect repo",
		Summary:  "workdir=/tmp/ws | orderId=2639076",
		Message:  "phase 2",
		Status:   "running",
		ToolName: "tool:start",
	}
	got, err := UpdateNarration(context.Background(), nil, ev)
	if err != nil {
		t.Fatalf("UpdateNarration() error = %v", err)
	}
	if got != "inspect repo: phase 2" {
		t.Fatalf("UpdateNarration() = %q", got)
	}
	got, err = UpdateNarration(context.Background(), &asynccfg.Config{
		Narration:         "template",
		NarrationTemplate: "{{intent}} {{summary}} -> {{message}}",
	}, ev)
	if err != nil {
		t.Fatalf("UpdateNarration(template) error = %v", err)
	}
	if got != "inspect repo workdir=/tmp/ws | orderId=2639076 -> phase 2" {
		t.Fatalf("UpdateNarration(template) = %q", got)
	}
	ctx := runtimerequestctx.WithUserAsk(context.Background(), "summarize progress")
	got, err = UpdateNarration(ctx, nil, ev)
	if err != nil {
		t.Fatalf("UpdateNarration(user ask) error = %v", err)
	}
	if got != "summarize progress: phase 2" {
		t.Fatalf("UpdateNarration(user ask) = %q", got)
	}
	ctx = WithLLMRunner(context.Background(), func(_ context.Context, in LLMInput) (string, error) {
		return in.UserAsk + "|" + in.Intent + "|" + in.Summary + "|" + in.Message, nil
	})
	got, err = UpdateNarration(ctx, &asynccfg.Config{Narration: "llm"}, ev)
	if err != nil {
		t.Fatalf("UpdateNarration(llm) error = %v", err)
	}
	if got != "|inspect repo|workdir=/tmp/ws | orderId=2639076|phase 2" {
		t.Fatalf("UpdateNarration(llm) = %q", got)
	}
	_, err = UpdateNarration(context.Background(), &asynccfg.Config{Narration: "llm"}, ev)
	if err == nil {
		t.Fatal("expected llm mode without runner to error")
	}
	got, err = UpdateNarration(context.Background(), &asynccfg.Config{Narration: "keydata"}, asynccfg.ChangeEvent{
		KeyData: []byte("<!-- DATA:hierarchy rows=1 -->\n<!-- DATA:delivery_impact rows=7 -->"),
	})
	if err != nil {
		t.Fatalf("UpdateNarration(keydata) error = %v", err)
	}
	if got != "Reviewing Hierarchy, Delivery impact." {
		t.Fatalf("UpdateNarration(keydata) = %q", got)
	}
	got, err = UpdateNarration(runtimerequestctx.WithUserAsk(context.Background(), "Troubleshoot 2654884"), &asynccfg.Config{Narration: "keydata"}, asynccfg.ChangeEvent{
		Message: "I’m translating the baseline limiting stack into forecastable parameters and reading each of the last three complete days separately.",
	})
	if err != nil {
		t.Fatalf("UpdateNarration(keydata direct message) error = %v", err)
	}
	if got != "I’m translating the baseline limiting stack into forecastable parameters and reading each of the last three complete days separately." {
		t.Fatalf("UpdateNarration(keydata direct message) = %q", got)
	}
}

func TestNarrationLLMEmptyFallsBackToDeterministicText(t *testing.T) {
	rec := &asynccfg.OperationRecord{
		OperationIntent:  "inspect repo",
		OperationSummary: "workdir=/tmp/ws | orderId=2639076",
		Message:          "phase 1",
		Status:           "running",
		ToolName:         "tool:start",
	}
	ctx := WithLLMRunner(context.Background(), func(_ context.Context, in LLMInput) (string, error) {
		return "   ", nil
	})
	got, err := StartNarration(ctx, &asynccfg.Config{Narration: "llm"}, rec)
	if err != nil {
		t.Fatalf("StartNarration(empty llm) error = %v", err)
	}
	if got != "inspect repo: phase 1" {
		t.Fatalf("StartNarration(empty llm) = %q", got)
	}
}

func TestFallbackUsesSummaryWhenIntentMissing(t *testing.T) {
	rec := &asynccfg.OperationRecord{
		OperationSummary: "workdir=/tmp/ws | orderId=2639076",
		Message:          "phase 1",
		Status:           "running",
		ToolName:         "tool:start",
	}
	got, err := StartNarration(context.Background(), nil, rec)
	if err != nil {
		t.Fatalf("StartNarration(summary fallback) error = %v", err)
	}
	if got != "workdir=/tmp/ws | orderId=2639076: phase 1" {
		t.Fatalf("StartNarration(summary fallback) = %q", got)
	}
}

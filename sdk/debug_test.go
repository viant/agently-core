package sdk

import (
	"context"
	"testing"

	"github.com/viant/agently-core/internal/logx"
)

func TestWithDebug_EnablesDebugLevelAndOptionalComponents(t *testing.T) {
	ctx := WithDebug(context.Background(), "conversation", "reactor")

	if !logx.EnabledAtWithContextForTest(ctx, logx.LevelDebug) {
		t.Fatalf("expected debug level to be enabled")
	}
	if !logx.ComponentEnabledWithContextForTest(ctx, "conversation") {
		t.Fatalf("expected conversation component to be enabled")
	}
	if !logx.ComponentEnabledWithContextForTest(ctx, "reactor") {
		t.Fatalf("expected reactor component to be enabled")
	}
	if logx.ComponentEnabledWithContextForTest(ctx, "scheduler") {
		t.Fatalf("did not expect scheduler component to be enabled")
	}
}

func TestWithDebugOptions_RespectsLevelAndComponentFilters(t *testing.T) {
	ctx := WithDebugOptions(
		context.Background(),
		WithDebugLevel("info"),
		WithDebugComponents("conversation"),
	)

	if !logx.EnabledAtWithContextForTest(ctx, logx.LevelInfo) {
		t.Fatalf("expected info level to be enabled")
	}
	if logx.EnabledAtWithContextForTest(ctx, logx.LevelDebug) {
		t.Fatalf("did not expect debug level to be enabled")
	}
	if !logx.ComponentEnabledWithContextForTest(ctx, "conversation") {
		t.Fatalf("expected conversation component to be enabled")
	}
	if logx.ComponentEnabledWithContextForTest(ctx, "reactor") {
		t.Fatalf("did not expect reactor component to be enabled")
	}
}

func TestWithDebugLevel_DefaultsUnknownValuesToDebug(t *testing.T) {
	ctx := WithDebugOptions(context.Background(), WithDebugLevel("something-unknown"))
	if !logx.EnabledAtWithContextForTest(ctx, logx.LevelDebug) {
		t.Fatalf("expected unknown debug level to fall back to debug")
	}
}

package sdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/viant/agently-core/internal/logx"
)

func TestDebugContextFromHeaders_EnablesScopedDebug(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/workspace/metadata", nil)
	req.Header.Set(HeaderDebugEnabled, "true")
	req.Header.Set(HeaderDebugLevel, "trace")
	req.Header.Set(HeaderDebugComponents, "conversation,reactor")

	ctx := debugContextFromHeaders(context.Background(), req)

	if !logx.EnabledAtWithContextForTest(ctx, logx.LevelTrace) {
		t.Fatalf("expected trace debug context to be enabled")
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

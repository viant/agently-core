package tool

import (
	"context"
	"strings"
	"testing"

	windowprotocol "github.com/viant/agently-core/protocol/window"
	"github.com/viant/agently-core/service/datasource"
	mcpclient "github.com/viant/mcp/client"
)

func TestLegacyClientBoundComponentAPIsFailWithoutDispatch(t *testing.T) {
	client := &authCaptureClient{}
	reg := &Registry{mgr: &executeAuthManagerStub{client: client}, internal: map[string]mcpclient.Interface{}, cache: map[string]*toolCacheEntry{}, recentResults: map[string]map[string]recentItem{}}
	if _, err := reg.ObserveNativeComponent(context.Background(), "old-producer", "read"); err == nil || !strings.Contains(err.Error(), "provider-owned resource/datasource") {
		t.Fatalf("legacy observation did not fail clearly: %v", err)
	}
	if out, err := reg.ExecuteNativeComponent(context.Background(), "old-producer", "read", windowprotocol.ComponentBinding{ID: "original", Revision: "release"}, nil); err == nil || out != nil || !strings.Contains(err.Error(), "provider-owned resource/datasource") {
		t.Fatalf("legacy pinned call did not fail clearly: %v", err)
	}
	if client.options != nil {
		t.Fatal("legacy pin dispatched an unpinned native call")
	}
	if dispatcher, err := datasource.NewNativeMCPComponentDispatcher(reg, []string{"old-producer"}); err == nil || dispatcher != nil {
		t.Fatal("legacy client transport activated")
	}
}

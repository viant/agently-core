package browsermcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/viant/agently-core/protocol/agui"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
)

func TestBrowserToolSignatureCacheIsIsolatedAndExpires(t *testing.T) {
	ctx := context.Background()
	descriptor := cfg.BrowserDescriptor{Name: "device", ExecutionLocation: "browser", Transport: cfg.BrowserTransport{Type: "chrome-extension", ExtensionID: strings.Repeat("a", 32), PortName: "generic-v1"}, AllowedTools: []string{"safe_*"}}
	r := New(func(context.Context) ([]cfg.BrowserDescriptor, error) {
		return []cfg.BrowserDescriptor{descriptor}, nil
	})
	input := Registration{ConversationID: "owned", ThreadID: "thread", Server: "device", ConnectionID: "connection", Tools: []Tool{{Name: "safe_read", Description: "Cached signature", InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`)}}}
	catalog, err := r.Register(ctx, "owner", input)
	if err != nil {
		t.Fatal(err)
	}
	// Caller-owned schema buffers and returned catalogs cannot alter the cache.
	input.Tools[0].InputSchema[0] = '!'
	catalog.Tools[0].Description = "changed"
	catalog.Tools[0].Parameters[0] = '!'
	for i := 0; i < 2; i++ {
		cached, err := r.Current(ctx, "owner", catalog.ID)
		if err != nil || cached.Hash != catalog.Hash || cached.Tools[0].Description != "[device/safe_read] Cached signature" {
			t.Fatalf("cached signature changed: %v", err)
		}
		if _, err := r.Validate(ctx, "owner", "thread", cached.Tools); err != nil {
			t.Fatal(err)
		}
		cached.Tools[0].Parameters[0] = '!'
	}
	r.mu.Lock()
	entry := r.catalogs[catalog.ID]
	entry.catalog.ExpiresAt = time.Now().Add(-time.Second)
	r.catalogs[catalog.ID] = entry
	r.mu.Unlock()
	if _, err := r.Current(ctx, "owner", catalog.ID); err == nil {
		t.Fatal("expired cached signature retained connection authority")
	}
}

func TestBrowserMCPCatalogOwnerThreadSchemaAndGeneration(t *testing.T) {
	descriptor := cfg.BrowserDescriptor{Name: "device", ExecutionLocation: "browser", Transport: cfg.BrowserTransport{Type: "chrome-extension", ExtensionID: strings.Repeat("a", 32), PortName: "generic-v1"}, AllowedTools: []string{"safe_*"}}
	registry := New(func(context.Context) ([]cfg.BrowserDescriptor, error) {
		return []cfg.BrowserDescriptor{descriptor}, nil
	})
	input := Registration{ConversationID: "owned", ThreadID: "wire-thread", Server: "device", ConnectionID: "connection1", Tools: []Tool{{Name: "safe_read", Description: "Fixture tool", InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`)}}}
	catalog, err := registry.Register(context.Background(), "owner", input)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Tools[0].Name != "device-safe_read" {
		t.Fatal("canonical server namespace changed")
	}
	if defs, err := registry.Validate(context.Background(), "owner", "wire-thread", catalog.Tools); err != nil || len(defs) != 1 {
		t.Fatal(err)
	}
	for _, scope := range [][2]string{{"other", "wire-thread"}, {"owner", "other-thread"}} {
		if _, err = registry.Validate(context.Background(), scope[0], scope[1], catalog.Tools); err == nil {
			t.Fatal("cross-owner/thread catalog admitted")
		}
	}
	for _, change := range []func(*agui.Tool){func(v *agui.Tool) { v.Parameters = json.RawMessage(`{"type":"object","additionalProperties":true}`) }, func(v *agui.Tool) { v.Name = "system_exec-run" }, func(v *agui.Tool) { v.Metadata = nil }, func(v *agui.Tool) { v.Description = "changed" }} {
		tool := catalog.Tools[0]
		change(&tool)
		if _, err = registry.Validate(context.Background(), "owner", "wire-thread", []agui.Tool{tool}); err == nil {
			t.Fatal("catalog substitution admitted")
		}
	}
	input.ConnectionID = "connection2"
	next, err := registry.Register(context.Background(), "owner", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = registry.Validate(context.Background(), "owner", "wire-thread", catalog.Tools); err == nil {
		t.Fatal("old connection retained authority")
	}
	descriptor.Transport.ExtensionID = strings.Repeat("b", 32)
	if _, err = registry.Current(context.Background(), "owner", next.ID); err == nil {
		t.Fatal("changed transport retained catalog")
	}
	input.Tools[0].Name = "forbidden"
	if _, err = registry.Register(context.Background(), "owner", input); err == nil {
		t.Fatal("name outside configured policy admitted")
	}
}
func TestBrowserMCPResultMetadataCannotSubstituteConnection(t *testing.T) {
	expected := json.RawMessage(`{"browserMCP":{"catalogId":"one","connectionId":"originating","server":"device","tool":"read"}}`)
	if VerifyResultMetadata("device-read", expected, expected) != nil {
		t.Fatal("exact metadata rejected")
	}
	if VerifyResultMetadata("device-read", expected, json.RawMessage(`{"browserMCP":{"catalogId":"one","connectionId":"other"}}`)) == nil {
		t.Fatal("substituted result connection accepted")
	}
}

func TestBrowserMCPResultMetadataDiagnosticsDoNotExposeValues(t *testing.T) {
	expected := json.RawMessage(`{"browserMCP":{"catalogId":"PRIVATE_CANARY","connectionId":"original","server":"device","tool":"read"}}`)
	for _, tc := range []struct {
		name        string
		want        string
		left, right json.RawMessage
	}{
		{"unexpected", "catalogResultMetadataUnexpected", nil, expected},
		{"missing", "catalogResultMetadataMissing", expected, nil},
		{"changed", "catalogResultMetadataChanged", expected, json.RawMessage(`{"browserMCP":{"catalogId":"PRIVATE_CANARY","connectionId":"replacement"}}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyResultMetadata("device-read", tc.left, tc.right)
			if !errors.Is(err, ErrUnavailable) || err.Error() != ErrUnavailable.Error()+" ("+tc.want+")" || strings.Contains(err.Error(), "PRIVATE_CANARY") {
				t.Fatalf("closed unavailable diagnostic required: %v", err)
			}
		})
	}
	if err := VerifyResultMetadata("plain", nil, nil); err != nil {
		t.Fatalf("ordinary tool metadata behavior changed: %v", err)
	}
}

func TestCurrentDiagnosticsAreClosedAndPreserveUnavailable(t *testing.T) {
	for _, mode := range []string{"catalogMissing", "catalogOwnerMismatch", "catalogExpired", "catalogConfigUnavailable", "catalogConfigChanged"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			descriptor := cfg.BrowserDescriptor{Name: "device", ExecutionLocation: "browser", AllowedTools: []string{"read"}}
			var providerErr error
			r := New(func(context.Context) ([]cfg.BrowserDescriptor, error) {
				return []cfg.BrowserDescriptor{descriptor}, providerErr
			})
			c, e := r.Register(ctx, "PRIVATE-owner", Registration{ConversationID: "PRIVATE-conversation", ThreadID: "PRIVATE-thread", Server: "device", ConnectionID: "PRIVATE-connection", Tools: []Tool{{Name: "read", InputSchema: json.RawMessage(`{"type":"object"}`)}}})
			if e != nil {
				t.Fatal(e)
			}
			user, id := "PRIVATE-owner", c.ID
			switch mode {
			case "catalogMissing":
				id = "PRIVATE-missing"
			case "catalogOwnerMismatch":
				user = "PRIVATE-other"
			case "catalogExpired":
				entry := r.catalogs[id]
				entry.catalog.ExpiresAt = time.Now().Add(-time.Second)
				r.catalogs[id] = entry
			case "catalogConfigUnavailable":
				providerErr = errors.New("PRIVATE secret configuration error")
			case "catalogConfigChanged":
				descriptor.AllowedTools = []string{"changed"}
			}
			actual, e := r.Current(ctx, user, id)
			if !errors.Is(e, ErrUnavailable) || e.Error() != ErrUnavailable.Error()+" ("+mode+")" || actual.ID != "" || strings.Contains(e.Error(), "PRIVATE") {
				t.Fatalf("unsafe diagnostic: %v", e)
			}
		})
	}
	var r *Registry
	if _, e := r.Current(context.Background(), "owner", "id"); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
}

func TestValidateDiagnosticsAndContinuationRoundTrip(t *testing.T) {
	modes := []string{"ready", "catalogRegistryUnavailable", "catalogConfigUnavailable", "catalogConfigChanged", "catalogMetadataMissing", "catalogMetadataInvalid", "catalogMissing", "catalogOwnerMismatch", "catalogThreadMismatch", "catalogExpired", "catalogDescriptionChanged", "catalogSchemaChanged", "catalogMetadataChanged", "catalogToolNotModelVisible", "catalogToolMissing"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			descriptor := cfg.BrowserDescriptor{Name: "device", ExecutionLocation: "browser", AllowedTools: []string{"read"}}
			var providerErr error
			r := New(func(context.Context) ([]cfg.BrowserDescriptor, error) {
				return []cfg.BrowserDescriptor{descriptor}, providerErr
			})
			input := Registration{ConversationID: "PRIVATE-conversation", ThreadID: "PRIVATE-thread", Server: "device", ConnectionID: "PRIVATE-connection", Tools: []Tool{{Name: "read", Description: "PRIVATE-description", InputSchema: json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`)}}}
			if mode == "catalogToolNotModelVisible" {
				input.Tools[0].Meta = json.RawMessage(`{"ui":{"visibility":["app"]}}`)
			}
			catalog, e := r.Register(context.Background(), "PRIVATE-owner", input)
			if e != nil {
				t.Fatal(e)
			}
			// The actual continuation serializes the same registered Tool objects again.
			raw, _ := json.Marshal(agui.RunAgentInput{ThreadID: input.ThreadID, RunID: "continuation", Tools: catalog.Tools, Messages: []agui.Message{{ID: "result", Role: "tool", ToolCallID: "original", Content: json.RawMessage(`"done"`), Metadata: catalog.Tools[0].Metadata}}})
			var continuation agui.RunAgentInput
			if e = json.Unmarshal(raw, &continuation); e != nil {
				t.Fatal(e)
			}
			tool := &continuation.Tools[0]
			user, thread := "PRIVATE-owner", input.ThreadID
			switch mode {
			case "catalogRegistryUnavailable":
				r = nil
			case "catalogConfigUnavailable":
				providerErr = errors.New("PRIVATE config error")
			case "catalogConfigChanged":
				descriptor.AllowedTools = []string{"changed"}
			case "catalogMetadataMissing":
				tool.Metadata = nil
			case "catalogMetadataInvalid":
				tool.Metadata = json.RawMessage(`{"browserMCP":{"catalogId":false}}`)
			case "catalogMissing":
				delete(r.catalogs, catalog.ID)
			case "catalogOwnerMismatch":
				user = "PRIVATE-other"
			case "catalogThreadMismatch":
				thread = "PRIVATE-other-thread"
			case "catalogExpired":
				entry := r.catalogs[catalog.ID]
				entry.catalog.ExpiresAt = time.Now().Add(-time.Second)
				r.catalogs[catalog.ID] = entry
			case "catalogDescriptionChanged":
				tool.Description = "PRIVATE-modified"
			case "catalogSchemaChanged":
				tool.Parameters = json.RawMessage(`{"type":"object"}`)
			case "catalogMetadataChanged":
				var m map[string]any
				json.Unmarshal(tool.Metadata, &m)
				m["extra"] = "PRIVATE-metadata"
				tool.Metadata, _ = json.Marshal(m)
			case "catalogToolMissing":
				tool.Name = "device-other"
			}
			definitions, e := r.Validate(context.Background(), user, thread, continuation.Tools)
			if mode == "ready" {
				if e != nil || len(definitions) != 1 {
					t.Fatal(e)
				}
				return
			}
			if !errors.Is(e, ErrUnavailable) || e.Error() != ErrUnavailable.Error()+" ("+mode+")" || strings.Contains(e.Error(), "PRIVATE") || definitions != nil {
				t.Fatalf("unsafe predicate diagnostic: %v", e)
			}
		})
	}
}

func TestCurrentStatusUsesOnlyFreshRegisteredConfigAndDoesNotHydrateTools(t *testing.T) {
	descriptor := cfg.BrowserDescriptor{Name: "device", ExecutionLocation: "browser", AllowedTools: []string{"read"}}
	allReads, singleReads := 0, 0
	var registry *Registry
	revokedDuringRead := false
	catalogIDForStatusTest := ""
	registry = New(func(context.Context) ([]cfg.BrowserDescriptor, error) {
		allReads++
		return []cfg.BrowserDescriptor{descriptor}, nil
	}, func(_ context.Context, name string) (cfg.BrowserDescriptor, error) {
		singleReads++
		if name != "device" {
			t.Fatal("caller selected another server")
		}
		if revokedDuringRead {
			_ = registry.Revoke("owner", catalogIDForStatusTest)
		}
		return descriptor, nil
	})
	catalog, err := registry.Register(context.Background(), "owner", Registration{ConversationID: "conversation", ThreadID: "thread", Server: "device", ConnectionID: "connection", Tools: []Tool{{Name: "read", Description: strings.Repeat("large fixture description ", 300), InputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	catalogIDForStatusTest = catalog.ID
	before := allReads
	started := time.Now()
	for i := 0; i < 20; i++ {
		status, e := registry.CurrentStatus(context.Background(), "owner", catalog.ID)
		if e != nil || status.Hash != catalog.Hash || status.ConnectionID != catalog.ConnectionID {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(status)
		if len(raw) > 512 || strings.Contains(string(raw), "description") || strings.Contains(string(raw), "tools") {
			t.Fatal("status hydrated catalog")
		}
	}
	if allReads != before || singleReads != 20 {
		t.Fatal("hot path enumerated unrelated configurations", allReads, singleReads)
	}
	t.Logf("20 fresh compact validations: %s", time.Since(started))
	descriptor.AllowedTools = []string{"changed"}
	if _, err = registry.CurrentStatus(context.Background(), "owner", catalog.ID); err == nil {
		t.Fatal("config change not observed")
	}
	descriptor.AllowedTools = []string{"read"}
	revokedDuringRead = true
	if _, err = registry.CurrentStatus(context.Background(), "owner", catalog.ID); err == nil {
		t.Fatal("revocation during config read lost")
	}
}

func TestCurrentCatalogRenewsIdleLifetimeOnlyForValidatedOwner(t *testing.T) {
	descriptor := cfg.BrowserDescriptor{Name: "device", ExecutionLocation: "browser", AllowedTools: []string{"read"}}
	registry := New(func(context.Context) ([]cfg.BrowserDescriptor, error) {
		return []cfg.BrowserDescriptor{descriptor}, nil
	})
	catalog, err := registry.Register(context.Background(), "owner", Registration{ConversationID: "conversation", ThreadID: "thread", Server: "device", ConnectionID: "connection", Tools: []Tool{{Name: "read", InputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(catalog.ExpiresAt) < 23*time.Hour {
		t.Fatal("new catalog still has short fixed expiry")
	}
	registry.mu.Lock()
	entry := registry.catalogs[catalog.ID]
	entry.catalog.ExpiresAt = time.Now().Add(time.Minute)
	registry.catalogs[catalog.ID] = entry
	before := entry.catalog.ExpiresAt
	registry.mu.Unlock()
	if _, e := registry.CurrentStatus(context.Background(), "foreign", catalog.ID); e == nil {
		t.Fatal("foreign owner accepted")
	}
	registry.mu.Lock()
	unchanged := registry.catalogs[catalog.ID].catalog.ExpiresAt.Equal(before)
	registry.mu.Unlock()
	if !unchanged {
		t.Fatal("foreign owner renewed catalog")
	}
	status, e := registry.CurrentStatus(context.Background(), "owner", catalog.ID)
	if e != nil || status.Hash != catalog.Hash || status.ConnectionID != catalog.ConnectionID || time.Until(status.ExpiresAt) < 23*time.Hour {
		t.Fatal("validated request failed to renew same catalog", e)
	}
	if e = registry.Revoke("owner", catalog.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = registry.CurrentStatus(context.Background(), "owner", catalog.ID); e == nil {
		t.Fatal("revoked catalog resurrected")
	}
}

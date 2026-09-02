package permittedview

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

type ToolExecutor interface {
	Execute(context.Context, string, map[string]interface{}) (string, error)
}

type MCPResolver struct {
	Executor ToolExecutor
	ToolName string
}

func (r *MCPResolver) Resolve(ctx context.Context, request *Request) (*Snapshot, error) {
	if r == nil || r.Executor == nil {
		return nil, fmt.Errorf("permitted view: authorization executor is unavailable")
	}
	toolName := strings.TrimSpace(r.ToolName)
	if toolName == "" {
		toolName = "steward:ResourceAuthorization"
	}
	args := map[string]interface{}{
		"ResourceType":                request.ResourceType,
		"ResourceIDs":                 request.ResourceIDs,
		"RequestedCapabilities":       request.RequestedCapabilities,
		"RequestedGlobalCapabilities": request.RequestedGlobalCapabilities,
		"IncludePrincipal":            request.IncludePrincipal,
	}
	raw, err := r.Executor.Execute(ctx, toolName, args)
	if err != nil {
		return nil, fmt.Errorf("permitted view: %s failed: %w", toolName, err)
	}
	result := &Snapshot{}
	if err = decodeSnapshot([]byte(raw), result); err != nil {
		return nil, fmt.Errorf("permitted view: decode authorization response: %w", err)
	}
	return result, nil
}

type Runtime struct {
	Resolver Resolver
	Now      func() time.Time
}

func NewRuntime(resolver Resolver) *Runtime {
	return &Runtime{Resolver: resolver, Now: time.Now}
}

func (r *Runtime) Apply(ctx context.Context, bound *BoundView) (*Result, error) {
	if bound == nil || bound.Window == nil {
		return nil, fmt.Errorf("permitted view: bound window is required")
	}
	if bound.Window.Authorization == nil {
		return Compile(bound, nil)
	}
	if r == nil || r.Resolver == nil {
		return nil, fmt.Errorf("permitted view: authorization runtime is unavailable")
	}
	request, err := ResolveRequest(bound)
	if err != nil {
		return nil, err
	}
	snapshot, err := r.Resolver.Resolve(ctx, request)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || strings.TrimSpace(snapshot.AuthorizationVersion) == "" {
		return nil, fmt.Errorf("permitted view: authorization snapshot/version is required")
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	if snapshot.ExpiresAt.IsZero() || !now.Before(snapshot.ExpiresAt) {
		return nil, fmt.Errorf("permitted view: authorization snapshot is expired")
	}
	if bound.ResourceID > 0 {
		resource := snapshot.Resources[fmt.Sprint(bound.ResourceID)]
		if resource != nil && (resource.ID != bound.ResourceID || !strings.EqualFold(resource.Type, bound.ResourceType)) {
			return nil, fmt.Errorf("permitted view: authorization resource identity mismatch")
		}
	}
	return Compile(bound, snapshot)
}

func decodeSnapshot(raw []byte, target *Snapshot) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	found := findSnapshot(value)
	if found == nil {
		return fmt.Errorf("authorization payload was not found")
	}
	encoded, _ := json.Marshal(found)
	return json.Unmarshal(encoded, target)
}

func findSnapshot(value any) map[string]any {
	switch actual := value.(type) {
	case map[string]any:
		if _, ok := actual["authorizationVersion"]; ok {
			return actual
		}
		for _, key := range []string{"data", "result", "output", "structuredContent"} {
			if nested := findSnapshot(actual[key]); nested != nil {
				return nested
			}
		}
		if content, ok := actual["content"].([]any); ok {
			for _, block := range content {
				if item, ok := block.(map[string]any); ok {
					if text, ok := item["text"].(string); ok {
						var decoded any
						if json.Unmarshal([]byte(text), &decoded) == nil {
							if nested := findSnapshot(decoded); nested != nil {
								return nested
							}
						}
					}
				}
			}
		}
	case []any:
		for _, item := range actual {
			if nested := findSnapshot(item); nested != nil {
				return nested
			}
		}
	}
	return nil
}

var defaultRuntimeState struct {
	sync.RWMutex
	runtime *Runtime
}

func SetDefaultRuntime(runtime *Runtime) func() {
	defaultRuntimeState.Lock()
	previous := defaultRuntimeState.runtime
	defaultRuntimeState.runtime = runtime
	defaultRuntimeState.Unlock()
	return func() {
		defaultRuntimeState.Lock()
		defaultRuntimeState.runtime = previous
		defaultRuntimeState.Unlock()
	}
}

func DefaultRuntime() *Runtime {
	defaultRuntimeState.RLock()
	defer defaultRuntimeState.RUnlock()
	return defaultRuntimeState.runtime
}

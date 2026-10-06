// Package mcpapps carries request-scoped host-only MCP results separately from
// model-safe tool text. No result enters model messages or shared agent state.
package mcpapps

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	mcpschema "github.com/viant/mcp-protocol/schema"
)

type captureKey struct{}
type recorderKey struct{}
type turnKey struct{}

func WithRecorder(ctx context.Context, recorder func(json.RawMessage, string) error) context.Context {
	return context.WithValue(ctx, recorderKey{}, recorder)
}
func WithTurnID(ctx context.Context, turnID string) context.Context {
	return context.WithValue(ctx, turnKey{}, turnID)
}
func TurnID(ctx context.Context) string { value, _ := ctx.Value(turnKey{}).(string); return value }

type Capture struct {
	mu                        sync.Mutex
	Server, Tool, OperationID string
	NativeOperationID         string
	Result                    json.RawMessage
}

func WithCapture(ctx context.Context, server, tool, operationID string) (context.Context, *Capture) {
	value := &Capture{Server: server, Tool: tool, OperationID: operationID}
	return context.WithValue(ctx, captureKey{}, value), value
}

// Record is called at the canonical registry's actual MCP response boundary.
// The exact selected server/method must match this one host request.
func Record(ctx context.Context, server, tool, nativeOperationID string, result *mcpschema.CallToolResult) error {
	capture, _ := ctx.Value(captureKey{}).(*Capture)
	if capture == nil {
		return nil
	}
	if server != capture.Server || tool != capture.Tool {
		return fmt.Errorf("MCP Apps result does not match scoped server/tool")
	}
	if result == nil {
		return nil
	}
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if len(data) > 4<<20 {
		return fmt.Errorf("MCP Apps host result exceeds 4MiB")
	}
	if recorder, ok := ctx.Value(recorderKey{}).(func(json.RawMessage, string) error); ok {
		if err := recorder(data, nativeOperationID); err != nil {
			return err
		}
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	capture.NativeOperationID = strings.TrimSpace(nativeOperationID)
	capture.Result = append(capture.Result[:0], data...)
	return nil
}
func (c *Capture) Snapshot() (json.RawMessage, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append(json.RawMessage(nil), c.Result...), c.NativeOperationID
}

func Active(ctx context.Context) bool {
	value, _ := ctx.Value(captureKey{}).(*Capture)
	return value != nil
}

// Scope is the exact native server/tool selected by the already authorized host
// binding. It is never reconstructed from a lossy provider tool alias.
func Scope(ctx context.Context) (string, string, bool) {
	capture, _ := ctx.Value(captureKey{}).(*Capture)
	if capture == nil {
		return "", "", false
	}
	return capture.Server, capture.Tool, true
}

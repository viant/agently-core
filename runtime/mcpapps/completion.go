package mcpapps

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sync"
)

type completionKey struct{}

// CompletionReceipt is private to one server-owned proxy invocation. It is
// neither serialized nor recovered from a journal or guest-supplied metadata.
type CompletionReceipt struct {
	mu                                                     sync.Mutex
	principal, conversation, turn, operation, server, tool string
	native                                                 string
	digest                                                 [sha256.Size]byte
	durable, completed, invalid                            bool
}

// WithCompletionReceipt is called only for a fresh, non-resumed host request.
// Each invocation gets a new receipt; identity and completion are never cached
// across requests or installed by a wire field.
func WithCompletionReceipt(ctx context.Context, principal, conversation, turn, operation, server, tool string) (context.Context, *CompletionReceipt) {
	receipt := &CompletionReceipt{principal: principal, conversation: conversation, turn: turn, operation: operation, server: server, tool: tool}
	return context.WithValue(ctx, completionKey{}, receipt), receipt
}

// RecordDurable is called by the trusted host recorder only after its exact
// native effect and full result were successfully committed to the journal.
func (r *CompletionReceipt) RecordDurable(native string, result json.RawMessage) {
	if r == nil || native == "" || len(result) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	digest := sha256.Sum256(result)
	if r.durable && (r.native != native || r.digest != digest) {
		r.invalid = true
		r.completed = false
		return
	}
	r.native = native
	r.digest = digest
	r.durable = true
}

// RecordCompletion follows successful full native guest-turn finalization.
// It also requires that the canonical capture belongs to this exact request
// and already has the same durably committed effect/result.
func RecordCompletion(ctx context.Context, principal, conversation, turn string) {
	receipt, _ := ctx.Value(completionKey{}).(*CompletionReceipt)
	capture, _ := ctx.Value(captureKey{}).(*Capture)
	if receipt == nil || capture == nil {
		return
	}
	result, native := capture.Snapshot()
	var outcome struct {
		IsError bool `json:"isError"`
	}
	if len(result) == 0 || json.Unmarshal(result, &outcome) != nil || outcome.IsError {
		return
	}
	receipt.mu.Lock()
	defer receipt.mu.Unlock()
	if principal == "" || conversation == "" || turn == "" || receipt.operation == "" || receipt.server == "" || receipt.tool == "" || !receipt.durable || receipt.invalid ||
		principal != receipt.principal || conversation != receipt.conversation || turn != receipt.turn ||
		capture.OperationID != receipt.operation || capture.Server != receipt.server || capture.Tool != receipt.tool || native == "" || native != receipt.native || sha256.Sum256(result) != receipt.digest {
		return
	}
	receipt.completed = true
}

// Matches permits reuse only for the exact successful completion in this same
// live invocation. Failed, missing, changed or recovered receipts fall through
// to the existing full finalizer.
func (r *CompletionReceipt) Matches(principal, conversation, turn, operation, native, status string, result json.RawMessage) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.completed && r.durable && !r.invalid && principal != "" && conversation != "" && turn != "" && operation != "" && native != "" && status == "succeeded" &&
		principal == r.principal && conversation == r.conversation && turn == r.turn && operation == r.operation && native == r.native && sha256.Sum256(result) == r.digest
}

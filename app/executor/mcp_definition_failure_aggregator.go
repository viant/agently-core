package executor

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/viant/agently-core/internal/logx"
	runtimectx "github.com/viant/agently-core/runtime/requestctx"
	resources "github.com/viant/agently-core/service/resource"
)

const (
	mcpDefinitionFailureWindow    = 30 * time.Second
	mcpDefinitionFailureGroupSize = 32
	mcpDefinitionFailureGroupMax  = 256
)

// The key contains the diagnostic fields that describe a failure. Request IDs
// identify individual attempts and are retained in the summary instead.
type mcpDefinitionFailureKey struct {
	Operation, Connection, Resource, Conversation, Turn string
	Category, Stage, Origin, Reason                     string
	CredentialType, CredentialPresent                   string
	RPCCode, RPCStatus, HTTPStatus                      int
}

type mcpDefinitionRequestPair struct {
	OperationID     string `json:"operationID"`
	RemoteRequestID string `json:"remoteRequestID"`
}

type mcpDefinitionFailureGroup struct {
	started  time.Time
	requests []mcpDefinitionRequestPair
	timer    *time.Timer
}

// mcpDefinitionFailureAggregator bounds repeated operational warnings. The
// first failure is logged immediately; a summary retains every request pair.
type mcpDefinitionFailureAggregator struct {
	mu     sync.Mutex
	groups map[mcpDefinitionFailureKey]*mcpDefinitionFailureGroup
	window time.Duration
	closed bool
}

func newMCPDefinitionFailureAggregator(window time.Duration) *mcpDefinitionFailureAggregator {
	return &mcpDefinitionFailureAggregator{groups: make(map[mcpDefinitionFailureKey]*mcpDefinitionFailureGroup), window: window}
}

func mcpDefinitionFailureDetails(ctx context.Context, failure resources.MCPReferenceFailure) (mcpDefinitionFailureKey, mcpDefinitionRequestPair) {
	turn, _ := runtimectx.TurnMetaFromContext(ctx)
	return mcpDefinitionFailureKey{
		Operation: failure.Operation, Connection: logx.SafeResourceIdentifier(failure.Connection),
		Resource: logx.SafeResourceIdentifier(failure.Resource), Conversation: runtimectx.ConversationIDFromContext(ctx), Turn: turn.TurnID,
		Category: failure.Category, Stage: failure.Stage, Origin: failure.Origin, Reason: failure.Reason,
		CredentialType: failure.CredentialType, CredentialPresent: failure.CredentialPresent,
		RPCCode: failure.RPCCode, RPCStatus: failure.RPCStatus, HTTPStatus: failure.HTTPStatus,
	}, mcpDefinitionRequestPair{OperationID: logx.SafeResourceIdentifier(failure.OperationID), RemoteRequestID: logx.SafeResourceIdentifier(failure.RemoteRequestID)}
}

func logMCPDefinitionFailureFields(key mcpDefinitionFailureKey, pair mcpDefinitionRequestPair) {
	logx.OperationalWarnf("resource", "mcp.definition.failed operation=%q connection=%q resource=%q convo=%q turn=%q category=%q rpcCode=%d rpcStatus=%d operationID=%q stage=%q origin=%q reason=%q credentialType=%q credentialPresent=%q httpStatus=%d remoteRequestID=%q",
		key.Operation, key.Connection, key.Resource, key.Conversation, key.Turn, key.Category, key.RPCCode, key.RPCStatus,
		pair.OperationID, key.Stage, key.Origin, key.Reason, key.CredentialType, key.CredentialPresent, key.HTTPStatus, pair.RemoteRequestID)
}

func (a *mcpDefinitionFailureAggregator) Record(ctx context.Context, failure resources.MCPReferenceFailure) {
	if failure.Category == "canceled" {
		return
	}
	key, pair := mcpDefinitionFailureDetails(ctx, failure)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		logMCPDefinitionFailureFields(key, pair)
		return
	}
	if group := a.groups[key]; group != nil {
		group.requests = append(group.requests, pair)
		if len(group.requests) >= mcpDefinitionFailureGroupSize {
			a.flushLocked(key, group)
		}
		return
	}
	if len(a.groups) >= mcpDefinitionFailureGroupMax {
		var oldestKey mcpDefinitionFailureKey
		var oldest *mcpDefinitionFailureGroup
		for candidateKey, candidate := range a.groups {
			if oldest == nil || candidate.started.Before(oldest.started) {
				oldestKey, oldest = candidateKey, candidate
			}
		}
		a.flushLocked(oldestKey, oldest)
	}
	logMCPDefinitionFailureFields(key, pair)
	group := &mcpDefinitionFailureGroup{started: time.Now(), requests: []mcpDefinitionRequestPair{pair}}
	a.groups[key] = group
	group.timer = time.AfterFunc(a.window, func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.groups[key] == group {
			a.flushLocked(key, group)
		}
	})
}

func (a *mcpDefinitionFailureAggregator) flushLocked(key mcpDefinitionFailureKey, group *mcpDefinitionFailureGroup) {
	if group == nil {
		return
	}
	delete(a.groups, key)
	if group.timer != nil {
		group.timer.Stop()
	}
	if len(group.requests) <= 1 {
		return
	}
	pairs, _ := json.Marshal(group.requests)
	logx.OperationalWarnf("resource", "mcp.definition.failed.summary operation=%q connection=%q resource=%q convo=%q turn=%q category=%q rpcCode=%d rpcStatus=%d stage=%q origin=%q reason=%q credentialType=%q credentialPresent=%q httpStatus=%d count=%d window=%q requestPairs=%s",
		key.Operation, key.Connection, key.Resource, key.Conversation, key.Turn, key.Category, key.RPCCode, key.RPCStatus,
		key.Stage, key.Origin, key.Reason, key.CredentialType, key.CredentialPresent, key.HTTPStatus,
		len(group.requests), a.window.String(), pairs)
}

func (a *mcpDefinitionFailureAggregator) Close() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	a.closed = true
	for key, group := range a.groups {
		a.flushLocked(key, group)
	}
}

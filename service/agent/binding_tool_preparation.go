package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sync"
	"time"

	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/internal/logx"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	resources "github.com/viant/agently-core/service/resource"
)

type bindingToolPreparationKey struct{}

type bindingToolPreparation struct {
	owner     *Service
	mu        sync.Mutex
	active    bool
	loaded    bool
	scope     [32]byte
	inventory []*toolbundle.Bundle
}

// Each binding owns one authored bundle inventory. Sibling resolution stages
// merge fresh maps from read-only bundle values; registry candidates and
// selections remain live.
// Nested and later bindings always establish a new scope.
func (s *Service) withBindingToolPreparation(ctx context.Context) context.Context {
	return context.WithValue(ctx, bindingToolPreparationKey{}, &bindingToolPreparation{owner: s, active: true})
}

func closeBindingToolPreparation(ctx context.Context) {
	if preparation, ok := ctx.Value(bindingToolPreparationKey{}).(*bindingToolPreparation); ok {
		preparation.mu.Lock()
		preparation.active = false
		preparation.inventory = nil
		preparation.mu.Unlock()
	}
}

func bindingToolPreparationScope(ctx context.Context) ([32]byte, bool) {
	turn, _ := requestctx.TurnMetaFromContext(ctx)
	var profileIdentity []string
	if profile := selectedPromptProfileFromContext(ctx); profile != nil {
		profileIdentity = []string{profile.ID, profile.ResourceURI, profile.ResourceProviderIdentity, profile.ResourceFingerprint}
	}
	// Keep contextual sources distinct without changing the context sent to them.
	// No credential, profile or payload data is emitted by stage diagnostics.
	raw, err := json.Marshal([]interface{}{
		authctx.User(ctx), authctx.CanonicalUserID(ctx), authctx.EffectiveUserID(ctx), authctx.Provider(ctx),
		authctx.Bearer(ctx), authctx.IDToken(ctx), authctx.MCPAuthToken(ctx, false), authctx.MCPAuthToken(ctx, true),
		requestctx.ConversationIDFromContext(ctx), turn.TurnID, profileIdentity, resources.DefinitionScopeFromContext(ctx),
	})
	return sha256.Sum256(raw), err == nil
}

func (s *Service) ambientToolBundles(ctx context.Context) (items []*toolbundle.Bundle, err error) {
	started := time.Now()
	reused := false
	defer func() { writeBindingToolStage(ctx, "bundle_inventory", started, len(items), reused, err) }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	read := func() ([]*toolbundle.Bundle, error) {
		if s.toolBundles != nil {
			return s.toolBundles(ctx)
		}
		if s.toolBundleRepo != nil {
			return s.toolBundleRepo.LoadAll(ctx)
		}
		return nil, nil
	}
	preparation, _ := ctx.Value(bindingToolPreparationKey{}).(*bindingToolPreparation)
	if preparation == nil || preparation.owner != s {
		return read()
	}
	scope, valid := bindingToolPreparationScope(ctx)
	if !valid {
		return read()
	}
	preparation.mu.Lock()
	defer preparation.mu.Unlock()
	if !preparation.active || preparation.loaded && preparation.scope != scope {
		return read()
	}
	if preparation.loaded {
		reused = true
		return preparation.inventory, nil
	}
	items, err = read()
	if err == nil && ctx.Err() == nil {
		preparation.inventory = append([]*toolbundle.Bundle(nil), items...)
		preparation.scope = scope
		preparation.loaded = true
	}
	return items, err
}

func writeBindingToolStage(ctx context.Context, stage string, started time.Time, count int, reused bool, err error) {
	if err != nil {
		turn, _ := requestctx.TurnMetaFromContext(ctx)
		code, status := diagnosticRPCFields(err)
		logx.OperationalWarnf("conversation", "binding.tool_stage.failed convo=%q turn=%q stage=%q errorType=%T rpcCode=%d rpcStatus=%d", requestctx.ConversationIDFromContext(ctx), turn.TurnID, stage, err, code, status)
	}
	if !debugtrace.Enabled() {
		return
	}
	turn, _ := requestctx.TurnMetaFromContext(ctx)
	debugtrace.Write("agent", "binding_tool_stage", map[string]interface{}{
		"conversationID": requestctx.ConversationIDFromContext(ctx), "turnID": turn.TurnID,
		"stage": stage, "elapsedMs": float64(time.Since(started)) / float64(time.Millisecond),
		"count": count, "reused": reused, "failed": err != nil,
	})
}

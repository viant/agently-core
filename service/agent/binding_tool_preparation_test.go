package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	convmem "github.com/viant/agently-core/internal/service/conversation/memory"
	"github.com/viant/agently-core/protocol/intent"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/toolselection"
	"github.com/viant/agently-core/service/intake"
)

func preparationConversation(tb testing.TB, s *Service) {
	tb.Helper()
	store := convmem.New()
	conv := apiconv.NewConversation()
	conv.SetId("conversation")
	conv.SetStatus("running")
	require.NoError(tb, store.PatchConversations(context.Background(), conv))
	s.conversation = store
}

func TestBindingPreparationReusesInventoryAndRefreshesNextBinding(t *testing.T) {
	s := selectionService()
	preparationConversation(t, s)
	original := s.toolBundles
	reads := 0
	changed := false
	s.toolBundles = func(ctx context.Context) ([]*toolbundle.Bundle, error) {
		reads++
		bundles, err := original(ctx)
		if changed && err == nil {
			bundles[0].Match = append(bundles[0].Match, llm.Tool{Name: "catalog:critical", AlwaysInclude: true})
		}
		return bundles, err
	}
	input := selectionInput(&intake.ToolSelection{Names: []string{"catalog:read"}, Complete: true})
	input.ConversationID, input.MessageID = "conversation", "turn"
	input.ToolBundles = []string{"workflow"}
	ctx := toolselection.WithState(context.Background(), nil)
	first, err := s.BuildBinding(ctx, input)
	require.NoError(t, err)
	require.Equal(t, 1, reads, "retention, resolution, validation and discovery share one inventory")
	require.ElementsMatch(t, []string{"catalog-read", "catalog-write"}, signatureNames(first.Tools.Signatures))
	first.Tools.Signatures[0].Parameters["original"] = "mutated"
	require.Equal(t, "schema", s.registry.(*fakeRegistry).defs[0].Parameters["original"], "binding schemas remain caller-owned copies")
	changed = true
	s.registry.(*fakeRegistry).defs = append(s.registry.(*fakeRegistry).defs, llm.ToolDefinition{Name: "catalog:critical"})
	input.Context[intake.ContextKey].(*intake.Context).Prompting.ToolSelection.Names = []string{"catalog:extra"}
	second, err := s.BuildBinding(ctx, input)
	require.NoError(t, err)
	require.Equal(t, 2, reads, "a later binding must observe a fresh authored inventory")
	require.ElementsMatch(t, []string{"catalog-extra", "catalog-write", "catalog-critical"}, signatureNames(second.Tools.Signatures))
	input.ToolsAllowed = []string{"catalog:write"}
	input.ToolBundles = nil
	third, err := s.BuildBinding(ctx, input)
	require.NoError(t, err)
	require.Equal(t, 3, reads)
	require.Equal(t, []string{"catalog-write"}, signatureNames(third.Tools.Signatures), "caller restrictions remain authoritative")
}

func TestBindingPreparationDoesNotFreezeRegistryPromotion(t *testing.T) {
	s := selectionService()
	reads := 0
	original := s.toolBundles
	s.toolBundles = func(ctx context.Context) ([]*toolbundle.Bundle, error) { reads++; return original(ctx) }
	ctx := toolselection.WithState(s.withBindingToolPreparation(context.Background()), nil)
	defer closeBindingToolPreparation(ctx)
	input := selectionInput(&intake.ToolSelection{Names: []string{"catalog:read"}, Complete: true})
	_, err := s.buildToolSignatures(ctx, input)
	require.NoError(t, err)
	s.registry.(*fakeRegistry).defs = append(s.registry.(*fakeRegistry).defs, llm.ToolDefinition{Name: "catalog:later"})
	input.Context[intake.ContextKey].(*intake.Context).Prompting.ToolSelection.Names = []string{"catalog:later"}
	definitions, err := s.buildToolSignatures(ctx, input)
	require.NoError(t, err)
	require.Equal(t, []string{"catalog-later"}, signatureNames(definitions))
	require.Equal(t, 1, reads, "only authored bundles are held; candidates remain live")
	state, _ := toolselection.FromContext(ctx)
	require.NoError(t, state.Activate([]string{"catalog-later"}))
	input.Context[intake.ContextKey].(*intake.Context).Prompting.ToolSelection.Names = []string{}
	definitions, err = s.buildToolSignatures(ctx, input)
	require.NoError(t, err)
	require.Contains(t, signatureNames(definitions), "catalog-later", "discovery promotion remains active")
}

func TestBindingPreparationReturnsIndependentProfileMergeMaps(t *testing.T) {
	s := selectionService()
	profile := &intent.Profile{ID: "chosen", ResourceURI: "intent://team/chosen", ToolBundles: []string{"base"}, ToolBundleDefinitions: []*toolbundle.Bundle{{ID: "base", Match: []llm.Tool{{Name: "catalog:read"}}}}}
	ctx := s.withBindingToolPreparation(withSelectedPromptProfile(context.Background(), profile))
	defer closeBindingToolPreparation(ctx)
	first, err := s.loadBundlesFor(ctx, []string{"workflow"})
	require.NoError(t, err)
	require.Equal(t, "catalog:read", first["base"].Match[0].Name)
	delete(first, "workflow")
	first["base"] = &toolbundle.Bundle{ID: "base", Match: []llm.Tool{{Name: "catalog:extra"}}}
	require.Equal(t, "catalog:read", profile.ToolBundleDefinitions[0].Match[0].Name, "mutating a merge map cannot alter admitted rules")
	first["injected"] = &toolbundle.Bundle{ID: "injected"}
	second, err := s.loadBundlesFor(ctx, []string{"workflow"})
	require.NoError(t, err)
	require.Contains(t, second, "workflow")
	require.NotContains(t, second, "injected")
	require.Equal(t, "catalog:read", second["base"].Match[0].Name, "ambient bundles cannot replace the owning profile")
	profile.ToolBundles = append(profile.ToolBundles, "missing")
	_, err = s.loadBundlesFor(ctx, []string{"missing"})
	require.ErrorContains(t, err, "admitted intent lacks declared tool bundle", "a missing owned attachment cannot fall back to ambient")
}

func TestBindingPreparationScopeAndFailuresDoNotLeak(t *testing.T) {
	s := selectionService()
	original := s.toolBundles
	reads := 0
	fail := true
	s.toolBundles = func(ctx context.Context) ([]*toolbundle.Bundle, error) {
		reads++
		if fail {
			return nil, errors.New("unavailable")
		}
		return original(ctx)
	}
	ctx := s.withBindingToolPreparation(cacheUser("alice", "one", "conversation"))
	_, err := s.ambientToolBundles(ctx)
	require.Error(t, err)
	fail = false
	_, err = s.ambientToolBundles(ctx)
	require.NoError(t, err)
	_, err = s.ambientToolBundles(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, reads, "failed reads are retried, successful inventory is reused")
	_, err = s.ambientToolBundles(authctx.WithBearer(ctx, "rotated"))
	require.NoError(t, err)
	_, err = s.ambientToolBundles(authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "bob"}))
	require.NoError(t, err)
	require.Equal(t, 4, reads)
	closeBindingToolPreparation(ctx)
	_, err = s.ambientToolBundles(ctx)
	require.NoError(t, err)
	require.Equal(t, 5, reads, "captured contexts cannot reuse a completed binding")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = s.ambientToolBundles(canceled)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 5, reads)
}

func TestBindingPreparationDiagnosticsContainOnlyStageMetrics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	t.Setenv("AGENTLY_DEBUG_TRACE_FILE", path)
	s := &Service{toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
		return []*toolbundle.Bundle{{ID: "private-bundle-name", Description: "private-content"}}, nil
	}}
	ctx := s.withBindingToolPreparation(requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{TurnID: "turn", ConversationID: "conversation"}))
	defer closeBindingToolPreparation(ctx)
	_, err := s.ambientToolBundles(ctx)
	require.NoError(t, err)
	_, err = s.ambientToolBundles(ctx)
	require.NoError(t, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private-bundle-name")
	require.NotContains(t, string(raw), "private-content")
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for _, reused := range []bool{false, true} {
		var record struct {
			Fields map[string]interface{} `json:"fields"`
		}
		require.NoError(t, decoder.Decode(&record))
		require.Equal(t, reused, record.Fields["reused"])
		require.Equal(t, "turn", record.Fields["turnID"])
		require.Equal(t, "conversation", record.Fields["conversationID"])
		require.Len(t, record.Fields, 7)
	}
}

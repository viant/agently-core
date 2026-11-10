package prompt

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/protocol/intent"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	intakerepo "github.com/viant/agently-core/workspace/repository/intake"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

func TestKnowledgeActivationIsOptInAndReceiptDoesNotDuplicateDocuments(t *testing.T) {
	for _, declared := range []bool{false, true} {
		for _, activate := range []bool{false, true} {
			repo := intakerepo.NewWithStore(fsstore.New(t.TempDir()))
			profile := &intent.Profile{ID: "guide", Name: "Guide", Messages: []intent.Message{{Role: "user", Text: "Explain the selected concept."}}}
			if declared {
				profile.Knowledge = []intent.KnowledgeMatch{{RootIDs: []string{"docs"}, Required: true}}
			}
			require.NoError(t, repo.Save(context.Background(), "guide", profile))
			client := &promptTestConversationClient{conversation: &apiconv.Conversation{}}
			service := New(repo, WithConversationClient(client))
			ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{ConversationID: "conversation", TurnID: "turn"})
			calls := 0
			ctx = WithToolActivation(ctx, nil, func(context.Context, *intent.Profile) ([]intent.Message, error) {
				calls++
				return []intent.Message{{Role: "system", URI: "workspace://docs/concept.md", Text: "Retrieved source fragment."}}, nil
			})
			out := &GetOutput{}
			require.NoError(t, service.get(ctx, &GetInput{ID: "guide", IncludeDocument: &activate}, out))
			if declared && activate {
				require.Equal(t, 1, calls)
				require.Len(t, client.messages, 2)
				require.Equal(t, "user", client.messages[0].Role)
				require.Equal(t, "system", client.messages[1].Role)
				require.Equal(t, "workspace://docs/concept.md", valueOrEmpty(client.messages[1].ContextSummary))
				require.Len(t, out.MessageIDs, 2)
				encoded, err := json.Marshal(out)
				require.NoError(t, err)
				require.NotContains(t, string(encoded), "Retrieved source fragment")
			} else {
				require.Zero(t, calls)
			}
		}
	}
}

func TestKnowledgeActivationRequiresConfiguredBinding(t *testing.T) {
	_, err := prepareKnowledgeActivation(context.Background(), &intent.Profile{Knowledge: []intent.KnowledgeMatch{{RootIDs: []string{"docs"}, Required: true}}})
	require.ErrorContains(t, err, "knowledge activation is unavailable")
	_, err = prepareKnowledgeActivation(context.Background(), &intent.Profile{})
	require.NoError(t, err)
}

func TestKnowledgeActivationFailureDoesNotInjectInstructions(t *testing.T) {
	repo := intakerepo.NewWithStore(fsstore.New(t.TempDir()))
	profile := &intent.Profile{ID: "guide", Name: "Guide", Messages: []intent.Message{{Role: "system", Text: "Use retrieved evidence."}}, Knowledge: []intent.KnowledgeMatch{{RootIDs: []string{"docs"}, Required: true}}}
	require.NoError(t, repo.Save(context.Background(), "guide", profile))
	client := &promptTestConversationClient{conversation: &apiconv.Conversation{}}
	service := New(repo, WithConversationClient(client))
	ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{ConversationID: "conversation", TurnID: "turn"})
	ctx = WithToolActivation(ctx, nil, func(context.Context, *intent.Profile) ([]intent.Message, error) {
		return nil, errors.New("knowledge source unavailable")
	})
	include := true
	out := &GetOutput{}
	err := service.get(ctx, &GetInput{ID: "guide", IncludeDocument: &include}, out)
	require.ErrorContains(t, err, "knowledge source unavailable")
	require.False(t, out.Injected)
	require.Empty(t, client.messages)
}

package sdk

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	agentsvc "github.com/viant/agently-core/service/agent"
	"testing"
)

type identityThreadResolver struct {
	aguistore.Store
	thread *aguistore.Thread
	err    error
	got    string
}

func (s *identityThreadResolver) GetThreadByConversationID(_ context.Context, _ string, id string) (*aguistore.Thread, error) {
	s.got = id
	return s.thread, s.err
}

func TestAGUINativeQueryUsesResolvedConversationAndPreservesWireBytes(t *testing.T) {
	wire := "  Case-雪\t"
	record := &aguistore.Run{ThreadID: wire, ConversationID: "native-uuid", TurnID: "native-turn", Principal: "owner"}
	query := &agentsvc.QueryInput{ConversationID: wire, MessageID: "native-turn", UserId: "owner"}
	require.NoError(t, bindAGUINativeQuery(record, query))
	require.Equal(t, "native-uuid", query.ConversationID)
	require.Equal(t, wire, record.ThreadID)
	require.NoError(t, bindAGUINativeQuery(record, query))
	bytes, err := json.Marshal(record)
	require.NoError(t, err)
	var public map[string]any
	require.NoError(t, json.Unmarshal(bytes, &public))
	require.Equal(t, wire, public["threadId"])
	require.NotContains(t, public, "conversationId")
}
func TestAGUINativeQueryRejectsMissingOrMismatchedBinding(t *testing.T) {
	for _, test := range []struct {
		name                            string
		native, user, turn, queryThread string
	}{
		{"missing native", "", "owner", "turn", "wire"},
		{"wrong principal", "native", "foreign", "turn", "wire"},
		{"wrong turn", "native", "owner", "foreign", "wire"},
		{"unbound scope", "native", "owner", "turn", "foreign"},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := &aguistore.Run{ThreadID: "wire", ConversationID: test.native, TurnID: "turn", Principal: "owner"}
			query := &agentsvc.QueryInput{ConversationID: test.queryThread, MessageID: test.turn, UserId: test.user}
			require.Error(t, bindAGUINativeQuery(record, query))
			require.Equal(t, test.queryThread, query.ConversationID)
		})
	}
}
func TestAGUINativeCallbackResolvesExactPublicBinding(t *testing.T) {
	resolver := &identityThreadResolver{thread: &aguistore.Thread{Principal: "owner", ThreadID: " wire \t", ConversationID: "native"}}
	wire, err := aguiWireThreadForConversation(context.Background(), resolver, "owner", "native")
	require.NoError(t, err)
	require.Equal(t, " wire \t", wire)
	require.Equal(t, "native", resolver.got)
	resolver.thread.ConversationID = "foreign"
	_, err = aguiWireThreadForConversation(context.Background(), resolver, "owner", "native")
	require.Error(t, err)
	resolver.err = aguistore.ErrNotFound
	wire, err = aguiWireThreadForConversation(context.Background(), resolver, "owner", "new-native")
	require.NoError(t, err)
	require.Equal(t, "new-native", wire)
}

// Fault-injection wrappers preserve the production identity capability while
// changing only the claimed lease or selected state-read boundary under test.
func (s *shortFirstLeaseStore) PromoteThread(ctx context.Context, p, t string) (*aguistore.Thread, error) {
	return s.Store.(aguistore.ThreadPromoter).PromoteThread(ctx, p, t)
}
func (s *shortFirstLeaseStore) GetThreadByConversationID(ctx context.Context, p, id string) (*aguistore.Thread, error) {
	return s.Store.(aguistore.ConversationThreadResolver).GetThreadByConversationID(ctx, p, id)
}
func (s *stateAdmissionCollisionStore) PromoteThread(ctx context.Context, p, t string) (*aguistore.Thread, error) {
	return s.Store.(aguistore.ThreadPromoter).PromoteThread(ctx, p, t)
}
func (s *stateAdmissionCollisionStore) GetThreadByConversationID(ctx context.Context, p, id string) (*aguistore.Thread, error) {
	return s.Store.(aguistore.ConversationThreadResolver).GetThreadByConversationID(ctx, p, id)
}

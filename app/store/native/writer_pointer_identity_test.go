package native_test

import (
	"context"
	"github.com/stretchr/testify/require"
	convwrite "github.com/viant/agently-core/internal/datly/conversation/write"
	msgwrite "github.com/viant/agently-core/internal/datly/message/write"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	"reflect"
	"testing"
)

func TestWriterOutputPreservesNativeRowPointerCorrespondence(t *testing.T) {
	store, _, db := orphanFixture(t)
	ctx := context.Background()
	first, second := &convwrite.MutableConversationView{}, &convwrite.MutableConversationView{}
	first.SetId("pointer-first")
	second.SetId("pointer-second")
	in := &convwrite.Input{}
	in.SetConversations([]*convwrite.MutableConversationView{first, second})
	value, err := store.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[convwrite.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/conversation"}}, Input: in})
	require.NoError(t, err)
	out := value.(*convwrite.Output)
	require.Len(t, out.Data, 2)
	require.Same(t, first, out.Data[0])
	require.Same(t, second, out.Data[1])
	require.NotNil(t, first.CreatedAt)
	_, err = db.Exec("INSERT INTO turn(id,conversation_id,status,created_at) VALUES('pointer-turn','pointer-first','succeeded','2026-01-01')")
	require.NoError(t, err)
	turn := "pointer-turn"
	m1, m2 := &msgwrite.Message{}, &msgwrite.Message{}
	for i, m := range []*msgwrite.Message{m1, m2} {
		m.SetId([]string{"pointer-m1", "pointer-m2"}[i])
		m.SetConversationId("pointer-first")
		m.SetTurnId(&turn)
		m.SetRole("assistant")
		m.SetType("text")
	}
	messages := &msgwrite.Input{}
	messages.SetMessages([]*msgwrite.Message{m2, m1})
	value, err = store.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[msgwrite.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/message"}}, Input: messages})
	require.NoError(t, err)
	mout := value.(*msgwrite.Output)
	require.Len(t, mout.Data, 2)
	require.Same(t, m2, mout.Data[0])
	require.Same(t, m1, mout.Data[1])
	require.NotNil(t, m1.Sequence)
	require.NotNil(t, m2.Sequence)
}

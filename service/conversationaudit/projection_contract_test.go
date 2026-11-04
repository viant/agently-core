package conversationaudit

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	countview "github.com/viant/agently-core/internal/datly/conversationaudit/count"
	projection "github.com/viant/agently-core/internal/datly/conversationaudit/read"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

func TestAuditNativeComponentsRequireTrustedScope(t *testing.T) {
	ctx := context.Background()
	s, _ := fixture(ctx, t)
	r := s.reader.(NativeReader)
	readInput := &projection.Input{}
	readInput.SetAllowed(true)
	readInput.SetOwnerID("")
	readInput.SetOwnerIDContains("")
	readInput.SetConversationID("own")
	readInput.SetFrom("")
	readInput.SetTo("")
	readInput.SetTurnID("")
	readInput.SetCursorTime(readInput.CursorTime)
	readInput.SetCursorID("")
	readInput.SetDetail(false)
	readInput.SetLimit(10)
	countInput := &countview.Input{}
	countInput.SetAllowed(true)
	countInput.SetOwnerID("")
	countInput.SetOwnerIDContains("")
	countInput.SetConversationID("own")
	countInput.SetFrom("")
	countInput.SetTo("")
	for _, tc := range []struct {
		name, scope, path string
		input             any
	}{
		{"read", reflect.TypeFor[projection.ReaderComponent]().PkgPath(), "/v1/internal/agently/conversation-audit", readInput},
		{"count", reflect.TypeFor[countview.ReaderComponent]().PkgPath(), "/v1/internal/agently/conversation-audit/count", countInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Even populated, presence-marked fields cannot replace auditaccess.
			_, err := r.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{
				Target: dexec.ComponentTarget{
					Component: spec.Key{Kind: spec.KindComponent, Scope: tc.scope, Name: "reader"},
					Route:     spec.RouteRef{Method: "GET", Path: tc.path},
				},
				Input: tc.input,
			})
			require.Error(t, err, "untrusted inputs must not provide audit scope")
		})
	}
	for _, input := range []*projection.Input{
		{ConversationID: "own", Limit: 10},
		{Allowed: true, Limit: 10},
		{Allowed: true, ConversationID: "own", Limit: 202},
		{Allowed: true, ConversationID: "own", Detail: true, Limit: 10},
	} {
		_, err := r.Read(ctx, input)
		require.Error(t, err, "generated read component must retain authored input validation")
	}
	for _, input := range []*projection.Input{{ConversationID: "own"}, {Allowed: true}} {
		_, err := r.Count(ctx, input)
		require.Error(t, err, "generated count component must retain authored input validation")
	}
}

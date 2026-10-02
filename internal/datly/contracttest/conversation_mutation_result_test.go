package tests

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	write "github.com/viant/agently-core/internal/datly/message/write"
	store "github.com/viant/agently-core/internal/store/conversation"
)

func TestMessageApplicationMutationResult(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	db, _ := messageFixture(t, filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(source)))))
	caller := &store.MessageStore{Invoker: messageWriterRuntime(t, db)}
	inserted := &write.Message{}
	inserted.SetId("result-message")
	inserted.SetConversationId("c1")
	turn := "t2"
	inserted.SetTurnId(&turn)
	inserted.SetRole("assistant")
	inserted.SetType("text")
	output, err := caller.PatchTrustedResult(context.Background(), inserted)
	must(t, err)
	if len(output.Data) != 1 || output.Data[0] == nil {
		t.Fatalf("missing mutation result: %#v", output)
	}
	row := output.Data[0]
	if row.CreatedAt == nil || row.Interim == nil || row.Sequence == nil || !row.Has.CreatedAt || !row.Has.Interim || !row.Has.Sequence {
		t.Fatalf("missing assigned values/presence: %#v %#v", row, row.Has)
	}
	sparse := &write.Message{}
	sparse.SetId(row.Id)
	content := "updated"
	sparse.SetContent(&content)
	output, err = caller.PatchTrustedResult(context.Background(), sparse)
	must(t, err)
	row = output.Data[0]
	if row.ConversationId != "c1" || row.TurnId == nil || *row.TurnId != "t2" || row.Role != "assistant" || row.Type != "text" || row.UpdatedAt == nil || !row.Has.ConversationId || !row.Has.TurnId || !row.Has.Role || !row.Has.Type || !row.Has.UpdatedAt {
		t.Fatalf("missing sparse mutation context: %#v %#v", row, row.Has)
	}
	if row.Sequence != nil || row.Has.Sequence || row.CreatedAt != nil || row.Has.CreatedAt {
		t.Fatalf("sparse result broadened supplied fields: %#v %#v", row, row.Has)
	}
	invalid := &write.Message{}
	invalid.SetId("invalid-result")
	invalid.SetConversationId("c1")
	invalid.SetType("text")
	failed, err := caller.PatchTrustedResult(context.Background(), invalid)
	if err == nil || failed != nil {
		t.Fatalf("failed mutation returned successful output: %#v %v", failed, err)
	}
}

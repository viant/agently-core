package dispatchpayload

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestAsyncProvenanceRetainsOriginalReferenceAndSharedIsolatedSessionLease(t *testing.T) {
	ctx, closeParent := WithState(context.Background())
	macro := "${artifact[2f1c171e-0fe9-4aa7-8f17-5eb826f72042].payload}"
	reads := 0
	ctx = WithDispatchPayloadResolver(ctx, func(ctx context.Context, name string, args map[string]interface{}) (map[string]interface{}, func(string) string, error) {
		reads++
		return args, func(s string) string { return "sanitized" }, nil
	})
	var closes atomic.Int32
	client := new(int)
	if !StoreSession(ctx, "owned-server", client, func() { closes.Add(1) }) {
		t.Fatal("missing ownedstate")
	}
	poll := CloneProvenance(ctx, context.Background())
	closeParent()
	if closes.Load() != 0 {
		t.Fatal("parentclosed asyncsession early")
	}
	session, ok := Session(poll, "owned-server")
	if !ok || session != client {
		t.Fatal("asyncpoll lost exact session")
	}
	args, sanitize, err := ResolveDispatchPayload(poll, "owned/status", map[string]interface{}{"operationId": "business-id"})
	if err != nil || args["operationId"] != "business-id" || sanitize("echo") != "sanitized" || reads != 1 {
		t.Fatal("poll lost original artifact revalidation/redaction callback")
	}
	if !HasArtifactReference(map[string]interface{}{"body": macro}) {
		t.Fatal("macro identity lost")
	}
	CloseProvenance(poll)
	if closes.Load() != 1 {
		t.Fatal("session notclosed once afterfinalowner")
	}
}

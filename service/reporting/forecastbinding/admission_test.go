package forecastbinding

import (
	"context"
	"encoding/json"
	authctx "github.com/viant/agently-core/internal/auth"
	"testing"
	"time"
)

func TestUserAdmissionIsTypedFrozenAndCannotSupplyAuthority(t *testing.T) {
	now := time.Date(2026, 10, 5, 1, 0, 0, 0, time.UTC)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"})
	client := json.RawMessage(`{"version":1,"selectedEntities":[{"kind":"audience","id":"7366798"}],"window":{"mode":"workspaceDefault"}}`)
	a, e := AdmitUserContext(ctx, "conversation", "turn", "starter", now, "America/Los_Angeles", client)
	if e != nil {
		t.Fatal(e)
	}
	if a.OwnerID != "owner" || a.Dates[0] != "2026-10-02" || a.Dates[2] != "2026-10-04" || a.SelectionOrigin != SelectionUser {
		t.Fatalf("bad admitted scope %+v", a)
	}
	plain, e := AdmitUserContext(ctx, "conversation", "turn", "starter", now, "UTC", nil)
	if e != nil || plain.SelectionOrigin != SelectionToolEvidence || plain.DateOrigin != DateToolEvidence || len(plain.Dates) != 0 {
		t.Fatal("plaintext silently guessed intent", e)
	}
	for _, bad := range []string{`{"version":1,"ownerId":"other"}`, `{"version":1,"selectionOrigin":"user-selection"}`, `{"version":1,"selectedEntities":[{"kind":"audience","id":"07366798"}]}`, `{"version":1,"window":{"mode":"explicit","from":"2026-10-05","to":"2026-10-04"}}`} {
		if _, e = AdmitUserContext(ctx, "conversation", "turn", "starter", now, "UTC", json.RawMessage(bad)); e == nil {
			t.Fatal("invalid input accepted", bad)
		}
	}
	if _, e = AdmitUserContext(context.Background(), "conversation", "turn", "starter", now, "UTC", client); e == nil {
		t.Fatal("unauthenticated admission accepted")
	}
}

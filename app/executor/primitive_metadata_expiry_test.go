package executor

import (
	"context"
	identity "github.com/viant/agently-core/protocol/resource"
	"testing"
	"time"
)

func TestPublicWindowMetadataDoesNotInheritCredentialExpiry(t *testing.T) {
	uri := "window://workspace/records"
	checks := 0
	selector := publicDefinitionRevision{uri: uri, actor: identity.VerifiedActor{ValidUntil: time.Now().Add(time.Minute)}, read: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error {
		checks++
		return nil
	}}
	decision, err := selector.SelectRevision(context.Background(), identity.ResourceRef{URI: uri}, []identity.ResourceCandidate{{Kind: identity.WorkingCandidate}})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.ValidUntil.IsZero() {
		t.Fatal("unchanged window metadata inherited the credential deadline")
	}
	if checks != 1 {
		t.Fatal("presentation visibility was not evaluated")
	}
}

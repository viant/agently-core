package reporting

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	iauth "github.com/viant/agently-core/internal/auth"
	identity "github.com/viant/agently-core/protocol/resource"
)

func TestNewReportUsesAuthenticatedCanonicalNamespace(t *testing.T) {
	service := New(Options{Store: NewMemoryStore(), NewID: func() string { return "new-id" }})
	input := &SaveReportRequest{ReportID: "caller-label", Title: "My report", ReportDocument: json.RawMessage(`{"title":"My report"}`)}
	before, _ := json.Marshal(input)
	ctx := iauth.WithCanonicalUserID(context.Background(), "canonical-Alice")
	ref, err := service.newReportRef(ctx, input)
	if err != nil || ref.URI != "report://users/canonical-Alice/caller-label" {
		t.Fatalf("incorrect user URI: %+v %v", ref, err)
	}
	after, _ := json.Marshal(input)
	if string(before) != string(after) {
		t.Fatal("save identity resolution mutated the caller's input")
	}
	for _, invalid := range []string{"", "alice/bob", "alice@example.test"} {
		if _, err := service.newReportRef(iauth.WithCanonicalUserID(context.Background(), invalid), input); err == nil {
			t.Fatalf("invalid canonical identity accepted: %q", invalid)
		}
	}
	generated, err := service.newReportRef(ctx, &SaveReportRequest{})
	if err != nil || generated.URI != "report://users/canonical-Alice/new-id" {
		t.Fatalf("unnamed report identity: %+v %v", generated, err)
	}
	// Scope enforcement belongs to the managing provider, not this resolver.
	explicit := &SaveReportRequest{Resource: &identity.ResourceRef{URI: "report://platform/shared"}}
	actual, err := service.newReportRef(ctx, explicit)
	if err != nil || !reflect.DeepEqual(actual, *explicit.Resource) {
		t.Fatalf("explicit managing-provider target changed: %+v %v", actual, err)
	}
}

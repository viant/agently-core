package feed

import (
	"context"
	"fmt"
	"testing"
)

func TestServiceMethodsRegistered(t *testing.T) {
	service := &Service{}
	for _, name := range []string{"get", "update"} {
		if method, err := service.Method(name); err != nil || method == nil {
			t.Fatalf("method %s not registered: %v", name, err)
		}
	}
}

func TestUpdateValidatesPatchContract(t *testing.T) {
	service := &Service{}
	output := &UpdateOutput{}
	if err := service.update(context.Background(), &UpdateInput{FeedID: "media-plan"}, output); err == nil {
		t.Fatal("expected empty operations to fail")
	}
	if err := service.update(context.Background(), &UpdateInput{FeedID: "media-plan", Operations: []Operation{{DataSourceRef: "draft", Op: "move", Path: "/budget"}}}, output); err == nil {
		t.Fatal("expected unsupported operation to fail")
	}
}

func TestFeedIdentityResolverPreservesLegacyAndPropagatesRoutingFailures(t *testing.T) {
	legacy := New(nil)
	id, err := legacy.feedIdentity(context.Background(), "media-plan")
	if err != nil || id != "media-plan" {
		t.Fatalf("legacy identity %q %v", id, err)
	}
	routed := New(nil, WithIdentityResolver(func(_ context.Context, requested string) (string, error) {
		if requested != "media-plan" {
			return "", fmt.Errorf("ambiguous or missing feed")
		}
		return "toolfeed://ownedfeeds/media-plan", nil
	}))
	id, err = routed.feedIdentity(context.Background(), "media-plan")
	if err != nil || id != "toolfeed://ownedfeeds/media-plan" {
		t.Fatalf("routing identity %q %v", id, err)
	}
	if _, err = routed.feedIdentity(context.Background(), "unknown"); err == nil {
		t.Fatal("resolver failure ignored")
	}
}

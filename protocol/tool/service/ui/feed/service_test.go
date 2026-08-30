package feed

import (
	"context"
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

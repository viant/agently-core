package server

import (
	"context"
	"errors"
	"testing"

	"github.com/viant/agently-core/app/executor"
)

func TestBuildWorkspaceRuntimeConfiguresBuilderBeforeBuild(t *testing.T) {
	want := errors.New("builder hook reached")
	called := false
	_, _, _, err := BuildWorkspaceRuntime(context.Background(), RuntimeOptions{WorkspaceRoot: t.TempDir(), ConfigureBuilder: func(_ context.Context, builder *executor.Builder) error {
		called = true
		if builder == nil {
			t.Fatal("nil builder")
		}
		return want
	}})
	if !called || !errors.Is(err, want) {
		t.Fatalf("hook called=%v error=%v", called, err)
	}
}

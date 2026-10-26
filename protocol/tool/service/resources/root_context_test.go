package resources

import (
	"context"
	"testing"

	"github.com/viant/agently-core/service/shared/toolexec"
)

func TestJoinBaseWithPath_RootSlash(t *testing.T) {
	base := "/tmp/root"
	ws := "workspace://localhost/root"
	got, err := joinBaseWithPath(ws, base, "/", "workspace://localhost/root")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != base {
		t.Fatalf("expected %s, got %s", base, got)
	}
}

func TestJoinBaseWithPath_RelativeAbsoluteLikeUnderFileRoot(t *testing.T) {
	ws := "workspace://localhost/workspace/example"
	base := "file:///workspace/example"
	got, err := joinBaseWithPath(ws, base, "workspace/example/docs/file.md", ws)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/workspace/example/docs/file.md" {
		t.Fatalf("unexpected joined path: %s", got)
	}
}

func TestToWorkspaceURI_DataDriven(t *testing.T) {
	type testCase struct {
		name     string
		input    string
		expected string
	}
	workspacePrefix := "workspace://localhost"
	cases := []testCase{
		{name: "relative path", input: "knowledge/bidder", expected: "knowledge/bidder"},
		{name: "simple segment", input: "bidder", expected: "bidder"},
		{name: "workspace uri passthrough", input: workspacePrefix + "/knowledge/bidder", expected: workspacePrefix + "/knowledge/bidder"},
		{name: "file uri converted", input: "file://localhost/tmp/doc.md", expected: workspacePrefix + "/tmp/doc.md"},
		{name: "abs path converted", input: "/tmp/doc.md", expected: workspacePrefix + "/tmp/doc.md"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := toWorkspaceURI(tc.input)
			if got != tc.expected {
				t.Fatalf("expected %s, got %s", tc.expected, got)
			}
		})
	}
}

func TestNewRootContext_ImplicitAllowedRoot(t *testing.T) {
	svc := &Service{}
	rc, err := svc.newRootContext(context.Background(), "", "", []string{"file:///workspace/example"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rc == nil {
		t.Fatalf("expected root context")
	}
	if got := rc.Workspace(); got != "file:///workspace/example" {
		t.Fatalf("expected file:///workspace/example, got %s", got)
	}
}

func TestNewRootContext_FallsBackFromFabricatedWorkspaceRoot(t *testing.T) {
	svc := &Service{}
	rc, err := svc.newRootContext(context.Background(), "workspace://localhost/", "", []string{"file:///workspace/example"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rc == nil {
		t.Fatalf("expected root context")
	}
	if got := rc.Workspace(); got != "file:///workspace/example" {
		t.Fatalf("expected file:///workspace/example, got %s", got)
	}
}

func TestInferAllowedRootFromPath(t *testing.T) {
	got := inferAllowedRootFromPath("/workspace/example/docs/file.md", []string{"file:///workspace/example"})
	if got != "file:///workspace/example" {
		t.Fatalf("unexpected inferred root: %s", got)
	}
}

func TestDefaultResourcePath_UsesWorkdirWhenPathMissing(t *testing.T) {
	ctx := toolexec.WithWorkdir(context.Background(), "/workspace/projects/poly/poly")
	if got := defaultResourcePath(ctx, ""); got != "/workspace/projects/poly/poly" {
		t.Fatalf("expected workdir fallback, got %s", got)
	}
	if got := defaultResourcePath(ctx, "/tmp/explicit"); got != "/tmp/explicit" {
		t.Fatalf("expected explicit path to win, got %s", got)
	}
}

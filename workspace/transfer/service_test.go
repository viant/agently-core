package transfer

import (
	"context"
	"os"
	"testing"

	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/store/fs"
)

func TestTransfer(t *testing.T) {
	os.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")
	defer os.Unsetenv("AGENTLY_WORKSPACE_NO_DEFAULTS")

	srcDir := t.TempDir()
	dstDir := t.TempDir()
	src := fs.New(srcDir)
	dst := fs.New(dstDir)
	ctx := context.Background()

	// Seed source with two agents and one model.
	if err := src.Save(ctx, workspace.KindAgent, "alpha", []byte("name: alpha")); err != nil {
		t.Fatalf("seed alpha: %v", err)
	}
	if err := src.Save(ctx, workspace.KindAgent, "beta", []byte("name: beta")); err != nil {
		t.Fatalf("seed beta: %v", err)
	}
	if err := src.Save(ctx, workspace.KindModel, "gpt4", []byte("provider: openai")); err != nil {
		t.Fatalf("seed gpt4: %v", err)
	}

	// Transfer all kinds.
	res, err := Transfer(ctx, src, dst, nil)
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if res.Copied != 3 {
		t.Errorf("expected 3 copied, got %d", res.Copied)
	}
	if len(res.Errors) != 0 {
		t.Errorf("unexpected errors: %v", res.Errors)
	}

	// Verify destination has the resources.
	for _, tc := range []struct{ kind, name, want string }{
		{workspace.KindAgent, "alpha", "name: alpha"},
		{workspace.KindAgent, "beta", "name: beta"},
		{workspace.KindModel, "gpt4", "provider: openai"},
	} {
		data, err := dst.Load(ctx, tc.kind, tc.name)
		if err != nil {
			t.Errorf("load %s/%s: %v", tc.kind, tc.name, err)
			continue
		}
		if string(data) != tc.want {
			t.Errorf("%s/%s = %q, want %q", tc.kind, tc.name, data, tc.want)
		}
	}
}

func TestTransfer_NoReplace(t *testing.T) {
	os.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")
	defer os.Unsetenv("AGENTLY_WORKSPACE_NO_DEFAULTS")

	srcDir := t.TempDir()
	dstDir := t.TempDir()
	src := fs.New(srcDir)
	dst := fs.New(dstDir)
	ctx := context.Background()

	if err := src.Save(ctx, workspace.KindAgent, "a", []byte("src")); err != nil {
		t.Fatal(err)
	}
	// Pre-populate destination so it should be skipped.
	if err := dst.Save(ctx, workspace.KindAgent, "a", []byte("dst")); err != nil {
		t.Fatal(err)
	}

	res, err := Transfer(ctx, src, dst, &Options{
		Kinds:   []string{workspace.KindAgent},
		Replace: false,
	})
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if res.Skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", res.Skipped)
	}
	if res.Copied != 0 {
		t.Errorf("expected 0 copied, got %d", res.Copied)
	}

	// Destination should still have original content.
	data, _ := dst.Load(ctx, workspace.KindAgent, "a")
	if string(data) != "dst" {
		t.Errorf("got %q, want %q", data, "dst")
	}
}

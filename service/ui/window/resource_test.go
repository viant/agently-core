package window

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/viant/agently-core/workspace"
	identity "github.com/viant/agently-core/protocol/resource"
	forgetypes "github.com/viant/forge/backend/types"
)

type workspaceRevisionPolicyFunc func(context.Context, identity.ResourceRef, []identity.ResourceCandidate) (identity.ResourceDecision, error)

func (f workspaceRevisionPolicyFunc) SelectRevision(ctx context.Context, ref identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	return f(ctx, ref, c)
}

func TestWorkspaceResourceHasOneWorkingCandidateAndPinsMergedAssets(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	previous := workspace.Root()
	workspace.SetRoot(other)
	t.Cleanup(func() { workspace.SetRoot(previous) })
	windowPath := filepath.Join(root, workspace.KindForgeWindow, "sales.yaml")
	datasourcePath := filepath.Join(root, workspace.KindForgeDataSource, "orders.yaml")
	mustWriteLoaderFile(t, windowPath, "windowKey: sales\nresources:\n  dataSources: [orders]\nview:\n  content: {id: sales, dataSourceRef: orders}\n")
	mustWriteLoaderFile(t, datasourcePath, "id: orders\ncardinality: collection\nbackend: {kind: inline, rows: []}\n")
	source, err := NewWorkspaceResourceSource(root, []ResourceBinding{{WindowKey: "sales", URI: "window://analytics/sales"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := source.Reference("sales")
	if err != nil {
		t.Fatal(err)
	}
	denied := false
	resolver := identity.ResourceResolver{Source: source, Policy: workspaceRevisionPolicyFunc(func(_ context.Context, _ identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
		if denied {
			return identity.ResourceDecision{}, identity.ErrResourceDenied
		}
		if len(c) != 1 || c[0].Kind != identity.WorkingCandidate || c[0].Revision != "" {
			t.Fatal("local YAML fabricated version history")
		}
		return identity.ResourceDecision{Candidate: c[0], ValidUntil: time.Now().Add(time.Minute), AuthorityBinding: "test-principal-account"}, nil
	})}
	pinned, err := resolver.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := resolver.ReadResolved(context.Background(), *pinned)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := forgetypes.SelectWindowResource(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	definition := selected.Window
	descriptors, err := LoadWorkspaceDatasourceDescriptorsAt(context.Background(), root, "sales", nil)
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := forgetypes.WindowDescriptorFingerprint(descriptors["orders"])
	if len(descriptors) != 1 || definition.ResourceDependencies["orders"] != expected {
		t.Fatal("report and window sources fingerprint different datasource descriptors")
	}
	if _, err = resolver.Resolve(context.Background(), identity.ResourceRef{URI: ref.URI, Revision: "r1"}); err == nil {
		t.Fatal("local YAML supplied nonexistent revision")
	}
	mustWriteLoaderFile(t, datasourcePath, "id: orders\ncardinality: collection\nbackend: {kind: inline, rows: [{id: changed}]}\n")
	if _, _, err = resolver.ReadResolved(context.Background(), *pinned); !errors.Is(err, identity.ErrResourceStale) {
		t.Fatalf("merged datasource drift: %v", err)
	}
	denied = true
	if _, err = resolver.Resolve(context.Background(), ref); err == nil {
		t.Fatal("working candidate bypassed policy")
	}
	if _, err = source.Reference("window://other/sales"); err == nil {
		t.Fatal("unknown namespace inherited a local key")
	}
}

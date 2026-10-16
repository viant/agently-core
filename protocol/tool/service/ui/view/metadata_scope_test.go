package view

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/viant/afs"
	repo "github.com/viant/agently-core/workspace/repository/forgewindow"
	forgeuisvc "github.com/viant/agently-core/service/primitiveprovider"
	identity "github.com/viant/agently-core/protocol/resource"
	forgetypes "github.com/viant/forge/backend/types"
)

type metadataScopeTestKey struct{}

type metadataScopeFixture struct {
	begins, finishes int
	bypasses         int
	finishErr        error
}

func (s *metadataScopeFixture) BeginMetadataRead(ctx context.Context) (context.Context, func() error, error) {
	s.begins++
	return context.WithValue(ctx, metadataScopeTestKey{}, s), func() error {
		s.finishes++
		return s.finishErr
	}, nil
}
func (s *metadataScopeFixture) WithoutMetadataRead(ctx context.Context) context.Context {
	s.bypasses++
	return ctx
}

type metadataScopeCatalog struct {
	listCalls, getCalls int
	listErr, getErr     error
}

func (*metadataScopeCatalog) AuthzReady() bool              { return true }
func (*metadataScopeCatalog) UsesResourceResolution() bool  { return true }
func (*metadataScopeCatalog) ConfiguredWindowIDs() []string { return []string{"public"} }
func (*metadataScopeCatalog) CheckWindowAdmission(context.Context, string) (bool, error) {
	return true, nil
}
func (*metadataScopeCatalog) ResourceReference(_ context.Context, key string) (identity.ResourceRef, error) {
	if key != "public" {
		return identity.ResourceRef{}, identity.ErrResourceDenied
	}
	return identity.ResourceRef{URI: "window://team/public"}, nil
}
func (c *metadataScopeCatalog) List(ctx context.Context, _ *forgeuisvc.WindowDefinitionListInput) (*forgeuisvc.WindowDefinitionListOutput, error) {
	c.listCalls++
	if ctx.Value(metadataScopeTestKey{}) == nil {
		return nil, errors.New("metadata list did not receive the scoped context")
	}
	if c.listErr != nil {
		return nil, c.listErr
	}
	return &forgeuisvc.WindowDefinitionListOutput{Windows: []forgeuisvc.WindowDefinitionSummary{{WindowID: "public", ResourceURI: "window://team/public"}}}, nil
}
func (c *metadataScopeCatalog) Get(ctx context.Context, in *forgeuisvc.WindowDefinitionGetInput) (*forgeuisvc.WindowDefinitionGetOutput, error) {
	c.getCalls++
	if ctx.Value(metadataScopeTestKey{}) == nil {
		return nil, errors.New("metadata get did not receive the scoped context")
	}
	if c.getErr != nil {
		return nil, c.getErr
	}
	if in == nil || in.WindowID != "public" {
		return nil, errors.New("unexpected window ID")
	}
	return &forgeuisvc.WindowDefinitionGetOutput{WindowID: "public", Definition: &forgetypes.Window{Resource: &identity.ResolvedResource{
		URI: "window://team/public", ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: strings.Repeat("a", 64)},
		AuthorityBinding: "verified-snapshot", ValidUntil: time.Now().Add(time.Minute),
	}}}, nil
}

func newMetadataScopeView(t *testing.T, scope *metadataScopeFixture, catalog *metadataScopeCatalog) *Service {
	t.Helper()
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{
		WindowDefinitions: catalog, MetadataScope: scope,
		DynamicWindowAuthorizer: func(context.Context, string) (bool, error) { return true, nil },
	})
	return New(repo.New(afs.New()), bridge, WithMetadataScope(scope))
}

func TestViewListAndGetUseOneOuterScopeForNestedForgeReads(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, root+"/extension/forge/windows/public.yaml", "id: public\ntitle: Public\nwindowKey: public\n")
		scope := &metadataScopeFixture{}
		catalog := &metadataScopeCatalog{}
		service := newMetadataScopeView(t, scope, catalog)
		listed := &ListOutput{Items: []ListItem{{ID: "stale"}}}
		if err := service.list(context.Background(), &ListInput{}, listed); err != nil {
			t.Fatal(err)
		}
		if scope.begins != 1 || scope.finishes != 1 || catalog.listCalls != 1 || catalog.getCalls != 1 || len(listed.Items) != 1 || listed.Items[0].ID != "public" {
			t.Fatalf("list scopes=%d/%d catalog=%d/%d output=%+v", scope.begins, scope.finishes, catalog.listCalls, catalog.getCalls, listed)
		}
		got := &GetOutput{Item: &ListItem{ID: "stale"}}
		if err := service.get(context.Background(), &GetInput{ID: "public"}, got); err != nil {
			t.Fatal(err)
		}
		if scope.begins != 2 || scope.finishes != 2 || got.Item == nil || got.Item.ID != "public" {
			t.Fatalf("get scopes=%d/%d output=%+v", scope.begins, scope.finishes, got)
		}
	})
}

func TestViewMetadataFailureFinalizationDiscardsOutput(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		mustWriteFile(t, root+"/extension/forge/windows/public.yaml", "id: public\ntitle: Public\nwindowKey: public\n")
		scope := &metadataScopeFixture{finishErr: errors.New("authority changed")}
		catalog := &metadataScopeCatalog{}
		service := newMetadataScopeView(t, scope, catalog)
		listed := &ListOutput{Items: []ListItem{{ID: "stale"}}}
		if err := service.list(context.Background(), &ListInput{}, listed); err == nil || listed.Items != nil || scope.begins != 1 || scope.finishes != 1 {
			t.Fatalf("list leaked after finish failure: err=%v output=%+v scopes=%d/%d", err, listed, scope.begins, scope.finishes)
		}
		got := &GetOutput{Item: &ListItem{ID: "stale"}}
		if err := service.get(context.Background(), &GetInput{ID: "public"}, got); err == nil || got.Item != nil || scope.begins != 2 || scope.finishes != 2 {
			t.Fatalf("get leaked after finish failure: err=%v output=%+v scopes=%d/%d", err, got, scope.begins, scope.finishes)
		}
		readFailure := errors.New("window metadata read failed")
		catalog.getErr = readFailure
		got = &GetOutput{Item: &ListItem{ID: "preexisting"}}
		err := service.get(context.Background(), &GetInput{ID: "public"}, got)
		if !errors.Is(err, readFailure) || !errors.Is(err, scope.finishErr) || got.Item != nil || scope.begins != 3 || scope.finishes != 3 {
			t.Fatalf("read/finalize failure did not discard output: err=%v output=%+v scopes=%d/%d", err, got, scope.begins, scope.finishes)
		}
	})
}

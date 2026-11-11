package workspace

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
)

type publishedStyleFixture struct {
	revision    int
	absent      bool
	failure     error
	lists, gets int
}

func (s *publishedStyleFixture) AdmissionKey(context.Context) (string, error) {
	return fmt.Sprint(s.revision), s.failure
}
func (s *publishedStyleFixture) Namespaces(context.Context) ([]primitive.NamespaceCapabilities, error) {
	return []primitive.NamespaceCapabilities{{ProviderIdentity: "public-owner", Namespace: "global", Kinds: []primitive.KindSupport{{Kind: "style", FormatVersions: []int64{1}, Operations: []string{"list", "get"}}}}}, s.failure
}
func (s *publishedStyleFixture) List(context.Context, string, string) ([]primitive.ResourceState, error) {
	s.lists++
	if s.failure != nil {
		return nil, s.failure
	}
	if s.absent {
		return []primitive.ResourceState{}, nil
	}
	return []primitive.ResourceState{{URI: "style://global/workspace", Kind: "style", Namespace: "global", Name: "workspace", ContentFingerprint: identity.ContentFingerprint([]byte(fmt.Sprint(s.revision)))}}, nil
}
func (s *publishedStyleFixture) Get(context.Context, string) (*primitive.ResourceState, error) {
	s.gets++
	return nil, errors.New("stylesheet bodies must not be read by layout")
}
func (s *publishedStyleFixture) CurrentHash(context.Context, string) (string, error) {
	return identity.ContentFingerprint([]byte(fmt.Sprint(s.revision))), s.failure
}

func TestGlobalStyleUsesPublishedIndexWithoutPrivateApplications(t *testing.T) {
	ctx := context.Background()
	trap := &layoutPublishedTrap{}
	pool, err := manager.New(trap)
	require.NoError(t, err)
	t.Cleanup(func() { pool.CloseConversation("") })
	actor, verify := resources.PublicReadActors()
	gateway := resources.NewGateway(pool, actor, verify, "host")
	t.Cleanup(gateway.Close)
	source := &publishedStyleFixture{revision: 1}
	require.NoError(t, gateway.RegisterPublishedMetadataSource(resources.Connection{Name: "public-catalog", ProviderIdentity: "public-owner"}, []string{"global"}, source))
	unrelated := &publishedStyleFixture{failure: context.DeadlineExceeded}
	require.NoError(t, gateway.RegisterPublishedMetadataSource(resources.Connection{Name: "unavailable-other", ProviderIdentity: "other-owner"}, []string{"other"}, unrelated))
	config := []byte("version: 1\nid: shell\napplications: []\n")
	reader, err := ApplicationMenuReader(gateway, config)
	require.NoError(t, err)
	handler := NewMetadataHandler(nil, nil, "test")
	handler.SetAuthorizationRuntimes(nil, nil)
	require.NoError(t, handler.SetCapturedLayout(t.TempDir(), "embedded", config, func(context.Context) error { return nil }))
	handler.SetLayoutMenuReader(reader)
	handler.starterTaskGateway = gateway
	handler.styles = nil
	first, err := handler.Layout(ctx, &LayoutRequest{})
	require.NoError(t, err)
	require.Empty(t, first.Layout.Applications)
	require.NotNil(t, first.GlobalStyle)
	require.Equal(t, "style://global/workspace", first.GlobalStyle.ResourceURI)
	require.Equal(t, "public-owner", first.GlobalStyle.ProviderIdentity)
	for i := 0; i < 10; i++ {
		next, err := handler.Layout(ctx, &LayoutRequest{})
		require.NoError(t, err)
		require.Equal(t, first.LayoutRevision, next.LayoutRevision)
		metadata, err := handler.Metadata(ctx)
		require.NoError(t, err)
		require.Equal(t, next.GlobalStyle, metadata.GlobalStyle)
		require.Equal(t, "managed", metadata.UIStyleSource)
		require.Nil(t, metadata.UIStyles)
	}
	require.Equal(t, 1, source.lists)
	require.Zero(t, source.gets)
	require.Zero(t, unrelated.lists)
	require.Zero(t, unrelated.gets)
	require.Zero(t, trap.options.Load())
	source.revision++
	changed, err := handler.Layout(ctx, &LayoutRequest{})
	require.NoError(t, err)
	require.NotEqual(t, first.GlobalStyle.StyleRevision, changed.GlobalStyle.StyleRevision)
	require.NotEqual(t, first.LayoutRevision, changed.LayoutRevision)
	require.Equal(t, 2, source.lists)
	source.revision++
	source.absent = true
	withdrawn, err := handler.Layout(ctx, &LayoutRequest{})
	require.NoError(t, err)
	require.Nil(t, withdrawn.GlobalStyle)
	metadata, err := handler.Metadata(ctx)
	require.NoError(t, err)
	require.Nil(t, metadata.GlobalStyle)
	require.NotEmpty(t, metadata.UIStyleDiagnostics)
	require.Empty(t, withdrawn.Layout.Applications)
	require.Zero(t, source.gets)
	require.Zero(t, unrelated.lists)
	require.Zero(t, unrelated.gets)
	require.Zero(t, trap.options.Load())

	source.failure = context.DeadlineExceeded
	unavailable, err := reader(ctx)
	require.NoError(t, err)
	require.Nil(t, unavailable.GlobalStyle)
	require.NotEmpty(t, unavailable.StyleDiagnostics)
	source.failure = nil
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = reader(cancelled)
	require.ErrorIs(t, err, context.Canceled)

}

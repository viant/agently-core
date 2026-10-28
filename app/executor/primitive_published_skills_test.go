package executor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	agent "github.com/viant/agently-core/protocol/agent"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	skills "github.com/viant/agently-core/service/skill"
	client "github.com/viant/mcp/client"
)

// Independently authored closed packages and transport traps exercise the real
// skill service without using producer captures or external applications.
type publishedSkillConsumerSource struct {
	raw    []byte
	denied bool
}

func (s *publishedSkillConsumerSource) AdmissionKey(ctx context.Context) (string, error) {
	if s.denied || authctx.EffectiveUserID(ctx) == "" {
		return "", identity.ErrResourceDenied
	}
	return "synthetic-session", nil
}
func (s *publishedSkillConsumerSource) CurrentHash(ctx context.Context, uri string) (string, error) {
	if _, err := s.AdmissionKey(ctx); err != nil {
		return "", err
	}
	if uri != "skill://example/review" {
		return "", identity.ErrResourceDenied
	}
	return identity.ContentFingerprint(s.raw), nil
}
func (s *publishedSkillConsumerSource) Namespaces(context.Context) ([]primitive.NamespaceCapabilities, error) {
	if s.denied {
		return nil, identity.ErrResourceDenied
	}
	return []primitive.NamespaceCapabilities{{ProviderIdentity: "publisher", Namespace: "example", Kinds: []primitive.KindSupport{{Kind: "skill", Operations: []string{"list", "get"}}}}}, nil
}
func (s *publishedSkillConsumerSource) List(ctx context.Context, kind, namespace string) ([]primitive.ResourceState, error) {
	if _, err := s.AdmissionKey(ctx); err != nil {
		return nil, err
	}
	if kind != "skill" || namespace != "" && namespace != "example" {
		return nil, nil
	}
	return []primitive.ResourceState{{URI: "skill://example/review", Kind: "skill", Namespace: "example", Name: "review", Title: "review", FormatVersion: 1, ContentFingerprint: identity.ContentFingerprint(s.raw)}}, nil
}
func (s *publishedSkillConsumerSource) Get(ctx context.Context, uri string) (*primitive.ResourceState, error) {
	if _, err := s.CurrentHash(ctx, uri); err != nil {
		return nil, err
	}
	rows, err := s.List(ctx, "skill", "example")
	if err != nil {
		return nil, err
	}
	result := rows[0]
	result.DefinitionBytes = append([]byte(nil), s.raw...)
	return &result, nil
}

type publishedSkillTransportTrap struct{ calls int }

func (*publishedSkillTransportTrap) Names(ctx context.Context) ([]string, error) {
	if authctx.EffectiveUserID(ctx) == "" {
		return nil, identity.ErrResourceDenied
	}
	return []string{"catalog"}, nil
}
func (*publishedSkillTransportTrap) CatalogNames(context.Context) ([]string, error) {
	return []string{"catalog"}, nil
}
func (p *publishedSkillTransportTrap) Options(context.Context, string) (*cfg.MCPClient, error) {
	p.calls++
	return nil, errors.New("unexpected remote metadata options")
}

func TestPublishedSkillConsumedFromClosedCohortWithoutRemoteMetadata(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "reader"})
	trap := &publishedSkillTransportTrap{}
	mgr, err := manager.New(trap, manager.WithClientFactory(func(context.Context, string, string) (client.Interface, error) {
		trap.calls++
		return nil, errors.New("unexpected metadata transport")
	}))
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation(""); mgr.CloseConversation("skill-catalog") })
	actor, verify := resources.PublicReadActors()
	gateway := resources.NewGateway(mgr, actor, verify, "host")
	t.Cleanup(gateway.Close)
	delivery, err := newPrimitiveSkillDelivery(ctx, gateway, nil)
	require.NoError(t, err)
	raw, err := json.Marshal(map[string]any{"schemaVersion": 1, "document": "---\nname: review\ndescription: Review synthetic examples\n---\nClosed package body.", "files": map[string]string{"refs/note.txt": "exact supporting bytes"}})
	require.NoError(t, err)
	source := &publishedSkillConsumerSource{raw: raw}
	require.NoError(t, gateway.RegisterPublishedMetadataSource(resources.Connection{Name: "catalog", ProviderIdentity: "publisher"}, []string{"example"}, source))
	service := skills.New(nil, nil, nil)
	service.SetDelivery(delivery.source, delivery)
	_, err = source.AdmissionKey(context.Background())
	require.ErrorIs(t, err, identity.ErrResourceDenied, "discovery must not depend on an anonymous caller being admitted")
	require.True(t, service.HasMCPSource(), "cohort-only skills must enable the configured agent skill activation path")
	require.Zero(t, trap.calls, "capability detection must not inspect remote options or negotiate a native endpoint")
	selected := &agent.Agent{Skills: []string{"*"}}
	page, err := service.ListVisiblePage(ctx, selected, primitive.CatalogQuery{Namespace: "example"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "skill://example/review", page.Items[0].URI)
	activated := service.VisibleSkillsWithContext(ctx, selected, []string{page.Items[0].URI})
	require.Len(t, activated, 1, "configured agent activation must consume cohort-only skills")
	require.Contains(t, activated[0].Body, "Closed package body.")
	item, err := service.GetVisible(ctx, selected, page.Items[0].URI)
	require.NoError(t, err)
	require.Contains(t, item.Body, "Closed package body.")
	require.Equal(t, "skill://example/review", item.CatalogURI)
	require.Equal(t, "skill://example/review/SKILL.md", item.RemoteURI)
	result, handled, err := delivery.ReadRemoteResource(ctx, item, "skill://example/review/refs/note.txt")
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, "exact supporting bytes", result.Contents[0].Text)
	require.Zero(t, trap.calls)
	// Lost admission cannot fall back to an endpoint or retained package.
	source.denied = true
	_, err = service.GetVisible(ctx, selected, page.Items[0].URI)
	require.Error(t, err)
	_, handled, err = delivery.ReadRemoteResource(ctx, item, "skill://example/review/refs/note.txt")
	require.True(t, handled)
	require.Error(t, err)
	require.Zero(t, trap.calls)
}

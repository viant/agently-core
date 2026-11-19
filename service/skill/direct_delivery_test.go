package skill

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	proto "github.com/viant/agently-core/protocol/skill"
)

type directReferenceDelivery struct {
	owner bool
	lists int
	reads int
}

type legacyQualifiedDelivery struct {
	Delivery
	item  *proto.Skill
	lists int
	reads int
}

func (d *legacyQualifiedDelivery) List(context.Context) ([]proto.Metadata, error) {
	d.lists++
	return []proto.Metadata{{URI: d.item.CatalogURI, Name: d.item.Frontmatter.Name, ServerID: d.item.ServerID}}, nil
}

func (d *legacyQualifiedDelivery) Read(context.Context, string) (*proto.Skill, bool, error) {
	d.reads++
	return d.item, true, nil
}

func (d *directReferenceDelivery) OwnsReference(string) bool { return d.owner }
func (*directReferenceDelivery) ReadLocal(context.Context, *proto.Skill) (*proto.Skill, error) {
	panic("unexpected local read")
}
func (d *directReferenceDelivery) List(context.Context) ([]proto.Metadata, error) {
	d.lists++
	return nil, errors.New("unrelated catalog unavailable")
}
func (d *directReferenceDelivery) Read(context.Context, string) (*proto.Skill, bool, error) {
	d.reads++
	return nil, true, errors.New("owning catalog denied")
}

func TestQualifiedRemoteReadDoesNotEnumerateUnrelatedPrimitiveCatalog(t *testing.T) {
	fixture := &skillMCPFixture{body: "---\nname: review\ndescription: Synthetic direct read\n---\nExact remote body."}
	service := &Service{mcpSource: &skillSourceFixture{f: fixture}, registry: proto.NewRegistry()}
	delivery := &directReferenceDelivery{}
	service.delivery = delivery
	actor := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	agent := &agentmdl.Agent{Skills: []string{"*"}}
	reference := (proto.Ref{ServerID: "github", Name: "review", ResourceURI: fixture.entry().Uri, Revision: manifestRevision(fixture.entry())}).URI()
	item, err := service.GetVisible(actor, agent, reference)
	require.NoError(t, err)
	require.Equal(t, "Exact remote body.", item.Body)
	require.Zero(t, delivery.lists)
	require.Zero(t, fixture.listCalls)
	require.Equal(t, 1, fixture.readCalls)
	fixture.body += " Changed."
	_, err = service.GetVisible(actor, agent, reference)
	require.ErrorContains(t, err, "skill changed")
	_, err = service.GetVisible(actor, &agentmdl.Agent{Skills: []string{"*", "!github/*"}}, reference)
	require.ErrorContains(t, err, "skill not available")
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	_, err = service.GetVisible(bob, agent, reference)
	require.Error(t, err)
	delivery.owner = true
	_, err = service.GetVisible(actor, agent, reference)
	require.ErrorContains(t, err, "owning catalog denied")
	require.Equal(t, 1, delivery.reads)
	require.Equal(t, 1, fixture.readCalls, "denied, revoked and changed identities must not deliver cached body bytes")
	require.Zero(t, delivery.lists, "qualified references retain their direct read path")
	delivery.owner = false
	for _, name := range []string{"github/review", "skill://team/review", "skill://github/review/SKILL.md"} {
		_, err = service.GetVisible(actor, agent, name)
		require.ErrorContains(t, err, "unrelated catalog unavailable")
	}
	require.Equal(t, 3, delivery.lists, "unqualified and document-alias selection keeps its existing catalog path")
	legacy := &legacyQualifiedDelivery{Delivery: delivery, item: &proto.Skill{CatalogURI: reference, ServerID: "github", Body: "legacy owning body", Frontmatter: proto.Frontmatter{Name: "review"}}}
	service.delivery = legacy
	item, err = service.GetVisible(actor, agent, reference)
	require.NoError(t, err)
	require.Equal(t, "legacy owning body", item.Body)
	require.Equal(t, 1, legacy.lists)
	require.Equal(t, 1, legacy.reads, "sources without an ownership classifier retain their original qualified selection")
}

func TestActiveCohortSkillDoesNotGateIndependentToolExecution(t *testing.T) {
	delivery := &directReferenceDelivery{owner: true}
	service := &Service{delivery: delivery}
	actor := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	agent := &agentmdl.Agent{Skills: []string{"*"}}
	execution := WithRuntimeState(actor, service, agent, []string{"skill://example/review"})
	for range 100 {
		require.NoError(t, ValidateExecution(execution, "catalog:read", nil))
		require.NoError(t, ValidateExecution(execution, "llm/tools:list", nil))
	}
	require.Zero(t, delivery.reads)
	require.Zero(t, delivery.lists)
	// Removing the owning source changes the next invocation's handling.
	delivery.owner = false
	require.Error(t, ValidateExecution(execution, "catalog:read", nil))
}

func TestActiveSkillPreservesCancellationIdentity(t *testing.T) {
	ctx := WithConstraints(context.Background(), &Constraints{Denied: true, Cause: context.Canceled})
	require.ErrorIs(t, ValidateExecution(ctx, "catalog:read", nil), context.Canceled)
	ctx = WithConstraints(context.Background(), &Constraints{Denied: true, Cause: context.DeadlineExceeded})
	require.ErrorIs(t, ValidateExecution(ctx, "catalog:read", nil), context.DeadlineExceeded)
}

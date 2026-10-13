package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	proto "github.com/viant/agently-core/protocol/skill"
	skills "github.com/viant/agently-core/service/skill"
	client "github.com/viant/mcp/client"
)

type skillControlSources struct{}

func (skillControlSources) Names(context.Context) ([]string, error) {
	return []string{"windows", "datasources", "skills"}, nil
}
func (skillControlSources) Options(_ context.Context, name string) (*cfg.MCPClient, error) {
	return &cfg.MCPClient{PrimitiveProviderIdentity: "owner:" + name}, nil
}
func (skillControlSources) Get(context.Context, string, string) (client.Interface, error) {
	return nil, fmt.Errorf("control discovery must not fetch a skill body")
}
func (skillControlSources) WithAuthTokenContext(ctx context.Context, _ string) context.Context {
	return ctx
}
func (skillControlSources) UseIDToken(context.Context, string) bool { return false }

type skillControlCatalog struct {
	rows     []proto.Metadata
	err      error
	observed context.Context
}

func (*skillControlCatalog) ReadLocal(context.Context, *proto.Skill) (*proto.Skill, error) {
	panic("body read during control discovery")
}
func (d *skillControlCatalog) List(ctx context.Context) ([]proto.Metadata, error) {
	d.observed = ctx
	return d.rows, d.err
}
func (*skillControlCatalog) Read(context.Context, string) (*proto.Skill, bool, error) {
	panic("body read during control discovery")
}

func TestSkillControlsUseOwningVisibleCatalogNotPrimitiveConnectionPresence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		rows      []proto.Metadata
		selection []string
		denied    bool
		visible   bool
	}{
		{name: "windows and datasources only", selection: []string{"*"}},
		{name: "skill supplied in mixed catalog", rows: []proto.Metadata{{Name: "review", URI: "skill://team/review/SKILL.md", ServerID: "skills"}}, selection: []string{"review"}, visible: true},
		{name: "unselected skill", rows: []proto.Metadata{{Name: "review", URI: "skill://team/review/SKILL.md", ServerID: "skills"}}, selection: []string{"other"}},
		{name: "excluded skill", rows: []proto.Metadata{{Name: "review", URI: "skill://team/review/SKILL.md", ServerID: "skills"}}, selection: []string{"*", "!review"}},
		{name: "managing catalog denial", selection: []string{"*"}, denied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := &skillControlCatalog{rows: tc.rows}
			if tc.denied {
				owner.err = fmt.Errorf("owning producer denied metadata")
			}
			skillService := skills.New(nil, nil, nil)
			skillService.SetDelivery(skillControlSources{}, owner)
			service := &Service{skillSvc: skillService}
			type marker struct{}
			ctx := context.WithValue(context.Background(), marker{}, "caller")
			visible, err := service.hasVisibleSkills(ctx, &agentmdl.Agent{Skills: tc.selection})
			require.Equal(t, tc.visible, visible)
			require.Equal(t, tc.denied, err != nil)
			require.Equal(t, ctx, owner.observed)
		})
	}
}

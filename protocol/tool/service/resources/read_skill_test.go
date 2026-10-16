package resources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	execconfig "github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	agmodel "github.com/viant/agently-core/protocol/agent"
	identity "github.com/viant/agently-core/protocol/resource"
	skillproto "github.com/viant/agently-core/protocol/skill"
	memory "github.com/viant/agently-core/runtime/requestctx"
	skillsvc "github.com/viant/agently-core/service/skill"
	"github.com/viant/agently-core/workspace"
)

func TestRead_SKILLMD_ActivatesVisibleSkill(t *testing.T) {
	rootURL := tempDirURL(t)
	rootPath := strings.TrimPrefix(rootURL, "file://")
	skillDir := filepath.Join(rootPath, "demo")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: Demo skill.\n---\n\nbody\n"), 0o644))

	agentID := "test-agent"
	convClient := convmem.New()
	conv := apiconv.NewConversation()
	conv.SetId("conv-1")
	conv.SetAgentId(agentID)
	require.NoError(t, convClient.PatchConversations(context.Background(), conv))

	agent := &agmodel.Agent{
		Identity: agmodel.Identity{ID: agentID},
		Resources: []*agmodel.Resource{
			{ID: "local", URI: rootURL, Role: "user"},
		},
		Skills: []string{"demo"},
	}
	skillService := skillsvc.New(&execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{rootPath}}}, convClient, &testAgentFinder{agent: agent})
	require.NoError(t, skillService.Load(context.Background()))

	svc := New(nil,
		WithConversationClient(convClient),
		WithAgentFinder(&testAgentFinder{agent: agent}),
		WithSkillService(skillService),
	)

	ctx := memory.WithConversationID(context.Background(), "conv-1")
	var out ReadOutput
	err := svc.read(ctx, &ReadInput{
		RootID: "local",
		Path:   "demo/SKILL.md",
		Mode:   "text",
	}, &out)
	require.NoError(t, err)
	require.Contains(t, out.Content, `Loaded skill "demo"`)
}

func TestRead_SKILLMD_ActivatesVisibleSkill_WithAbsoluteRoot(t *testing.T) {
	rootURL := tempDirURL(t)
	rootPath := strings.TrimPrefix(rootURL, "file://")
	skillDir := filepath.Join(rootPath, "skills", "demo")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: Demo skill.\n---\n\nbody\n"), 0o644))

	agentID := "test-agent"
	convClient := convmem.New()
	conv := apiconv.NewConversation()
	conv.SetId("conv-1")
	conv.SetAgentId(agentID)
	require.NoError(t, convClient.PatchConversations(context.Background(), conv))

	agent := &agmodel.Agent{
		Identity: agmodel.Identity{ID: agentID},
		Resources: []*agmodel.Resource{
			{ID: "local", URI: rootPath, Role: "user"},
		},
		Skills: []string{"demo"},
	}
	skillService := skillsvc.New(&execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{filepath.Join(rootPath, "skills")}}}, convClient, &testAgentFinder{agent: agent})
	require.NoError(t, skillService.Load(context.Background()))

	svc := New(nil,
		WithConversationClient(convClient),
		WithAgentFinder(&testAgentFinder{agent: agent}),
		WithSkillService(skillService),
	)

	ctx := memory.WithConversationID(context.Background(), "conv-1")
	var out ReadOutput
	err := svc.read(ctx, &ReadInput{
		RootID: "local",
		Path:   "skills/demo/SKILL.md",
		Mode:   "text",
	}, &out)
	require.NoError(t, err)
	require.Contains(t, out.Content, `Loaded skill "demo"`)
}

type deniedNativeSkillDelivery struct{}

func (deniedNativeSkillDelivery) ReadLocal(context.Context, *skillproto.Skill) (*skillproto.Skill, error) {
	return nil, identity.ErrResourceDenied
}
func (deniedNativeSkillDelivery) List(context.Context) ([]skillproto.Metadata, error) {
	return nil, nil
}
func (deniedNativeSkillDelivery) Read(context.Context, string) (*skillproto.Skill, bool, error) {
	return nil, false, nil
}
func TestRead_SKILLMD_DeniedDeliveryDoesNotFallBackToFilesystem(t *testing.T) {
	rootURL := tempDirURL(t)
	rootPath := strings.TrimPrefix(rootURL, "file://")
	skillDir := filepath.Join(rootPath, "skills", "demo")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: Demo skill.\n---\n\nbody\n"), 0o644))

	agentID := "test-agent"
	convClient := convmem.New()
	conv := apiconv.NewConversation()
	conv.SetId("conv-1")
	conv.SetAgentId(agentID)
	require.NoError(t, convClient.PatchConversations(context.Background(), conv))

	agent := &agmodel.Agent{
		Identity: agmodel.Identity{ID: agentID},
		Resources: []*agmodel.Resource{
			{ID: "local", URI: rootURL, Role: "user"},
		},
		Skills: []string{"demo"},
	}
	skillService := skillsvc.New(&execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{filepath.Join(rootPath, "skills")}}}, convClient, &testAgentFinder{agent: agent})
	require.NoError(t, skillService.Load(context.Background()))
	skillService.SetDelivery(nil, deniedNativeSkillDelivery{})

	svc := New(nil,
		WithConversationClient(convClient),
		WithAgentFinder(&testAgentFinder{agent: agent}),
		WithSkillService(skillService),
		WithRawResourceBoundary(workspace.NewRawResourceBoundary(rootPath)),
	)

	ctx := memory.WithConversationID(context.Background(), "conv-1")
	var out ReadOutput
	err := svc.read(ctx, &ReadInput{
		RootID: "local",
		Path:   "skills/demo/SKILL.md",
		Mode:   "text",
	}, &out)
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	require.Empty(t, out.Content)
}

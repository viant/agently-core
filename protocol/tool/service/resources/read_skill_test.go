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
	memory "github.com/viant/agently-core/runtime/requestctx"
	skillsvc "github.com/viant/agently-core/service/skill"
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

package server

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	cfg "github.com/viant/agently-core/app/executor/config"
	authctx "github.com/viant/agently-core/internal/auth"
	skill "github.com/viant/agently-core/service/skill"
	"os"
	"path/filepath"
	"testing"
)

func TestExposeLocalSkillsAndResources(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "review")
	require.NoError(t, os.MkdirAll(dir, 0700))
	body := "---\nname: review\ndescription: Review changes\n---\nRead carefully."
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0600))
	svc := skill.New(&cfg.Defaults{Skills: cfg.SkillsDefaults{Roots: []string{root}}}, nil, nil)
	require.NoError(t, svc.Load(context.Background()))
	adapter := &runtimeExecutorAdapter{rt: &executor.Runtime{Skills: svc}, skillItems: []string{"review"}}
	_, err := adapter.ListSkills(context.Background(), nil)
	require.Error(t, err)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	list, err := adapter.ListSkills(ctx, nil)
	require.NoError(t, err)
	require.Len(t, list.Skills, 1)
	require.Equal(t, "skill://agently/review/SKILL.md", list.Skills[0].Uri)
	got, err := adapter.GetSkill(ctx, list.Skills[0].Uri)
	require.NoError(t, err)
	require.Equal(t, list.Skills[0], got.Skill)
	read, err := adapter.ReadResource(ctx, got.Skill.Uri)
	require.NoError(t, err)
	require.Equal(t, body, read.Contents[0].Text)
	_, err = adapter.GetSkill(ctx, "skill://agently/hidden/SKILL.md")
	require.Error(t, err)
}

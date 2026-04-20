package skill

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	execconfig "github.com/viant/agently-core/app/executor/config"
	agentmdl "github.com/viant/agently-core/protocol/agent"
)

func TestWatcherReloadsSkillsOnChange(t *testing.T) {
	root := t.TempDir()
	skillsRoot := filepath.Join(root, "skills")
	if err := os.MkdirAll(filepath.Join(skillsRoot, "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkill := func(desc string) {
		content := "---\nname: demo\ndescription: " + desc + "\n---\n\nbody\n"
		if err := os.WriteFile(filepath.Join(skillsRoot, "demo", "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSkill("first")
	svc := New(&execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{skillsRoot}}}, nil, nil)
	if err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := NewWatcher(svc)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	writeSkill("second")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		meta, _ := svc.Visible(&agentmdl.Agent{Skills: []string{"demo"}})
		if len(meta) == 1 && meta[0].Description == "second" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	meta, _ := svc.Visible(&agentmdl.Agent{Skills: []string{"demo"}})
	t.Fatalf("watcher did not reload skill, got %#v", meta)
}

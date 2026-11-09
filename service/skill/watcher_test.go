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
	w.interval = 10 * time.Millisecond
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

func TestManifestSnapshotUsesOnlyLoadableSkillLocations(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"alpha/SKILL.md", "scratch/build/SKILL.md", "alpha/references/notes.md"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := manifestSnapshot([]string{root, filepath.Join(root, "absent")})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot[filepath.Join(root, "alpha", "SKILL.md")]; !ok || len(snapshot) != 1 {
		t.Fatalf("unexpected manifest files: %v", snapshot)
	}
}

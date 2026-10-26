package loader

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viant/afs"
	"github.com/stretchr/testify/require"
	meta "github.com/viant/agently-core/workspace/service/meta"
)

func TestAgentMarkdownPreservesYAMLFieldsAndPromptSlots(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agents")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	definition := `---
id: reviewer
name: Reviewer
model: gpt-4o
description: Reviews findings
skills: [audit]
---
---
role: system
field: systemPrompt
engine: velty
uri: templates/base.md
---
---
role: system
---
# Instructions
Use evidence.
---
role: user
field: prompt
---
# Request
Summarize the result.
`
	if err := os.WriteFile(filepath.Join(dir, "reviewer.md"), []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	svc := New(WithMetaService(meta.New(afs.New(), root)))
	for _, name := range []string{"reviewer", "reviewer.md"} {
		got, err := svc.Load(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != "reviewer" || got.Name != "Reviewer" || got.Description != "Reviews findings" || len(got.Skills) != 1 || got.Skills[0] != "audit" {
			t.Fatalf("frontmatter fields changed: %+v", got)
		}
		if got.InstructionPrompt == nil || got.InstructionPrompt.Text != "# Instructions\nUse evidence." || got.Prompt == nil || got.Prompt.Text != "# Request\nSummarize the result.\n" {
			t.Fatalf("Markdown body was not mapped to prompt slots: %+v", got)
		}
		if got.SystemPrompt == nil || got.SystemPrompt.Engine != "velty" || !strings.HasSuffix(got.SystemPrompt.URI, "/agents/templates/base.md") {
			t.Fatalf("URI-backed system prompt lost its source: %+v", got.SystemPrompt)
		}
	}
}

func TestAgentMarkdownRejectsConflictsAndDuplicateFormats(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agents")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	svc := New(WithMetaService(meta.New(afs.New(), root)))
	write("reviewer.md", "---\nid: reviewer\ninstructionPrompt: existing\n---\n# Instructions\nbody")
	if _, err := svc.Load(context.Background(), "reviewer"); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("frontmatter/body instruction conflict accepted: %v", err)
	}
	write("reviewer.md", "---\nid: reviewer\n---\n---\nrole: assistant\n---\nanswer")
	if _, err := svc.Load(context.Background(), "reviewer"); err == nil || !strings.Contains(err.Error(), "assistant") {
		t.Fatalf("unsupported assistant field accepted: %v", err)
	}
	write("reviewer.md", "---\nid: reviewer\n---\nbody")
	write("reviewer.yaml", "id: reviewer\n")
	if _, err := svc.Load(context.Background(), "reviewer"); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate file identity accepted: %v", err)
	}
	if _, err := svc.LoadAgents(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate directory definitions accepted: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "reviewer.yaml")); err != nil {
		t.Fatal(err)
	}
	write("other.yaml", "id: reviewer\nname: Other\n")
	if _, err := svc.LoadAgents(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate declared agent ID accepted: %v", err)
	}
}

func TestLoadAgentsIncludesNestedDefinitionsButNotKnowledgeMarkdown(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"agents/alpha", "agents/beta", "agents/beta/knowledge"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{
		"agents/alpha/alpha.yaml":        "id: alpha\nname: Alpha\n",
		"agents/beta/beta.md":            "---\nid: beta\nname: Beta\n---\n# Instructions\nAnswer.\n",
		"agents/beta/knowledge/SETUP.md": "# Supporting knowledge, not an agent\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	svc := New(WithMetaService(meta.New(afs.New(), root)))
	loaded, err := svc.LoadAgents(context.Background(), "agents")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 || loaded[0].ID != "alpha" || loaded[1].ID != "beta" {
		t.Fatalf("nested definitions were not discovered exactly once: %+v", loaded)
	}
}

func TestAgentPlaybookAllowListPreferredAndLegacyAliases(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agents")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	svc := New(WithMetaService(meta.New(afs.New(), root)))
	for name, body := range map[string]string{
		"preferred.md": "---\nid: preferred\nplaybooks: {bundles: [review], injectSelectedProfile: false}\n---\n",
		"legacy.yaml":  "id: legacy\nintents: {bundles: [review]}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	preferred, err := svc.Load(context.Background(), "preferred")
	if err != nil || len(preferred.Playbooks.Bundles) != 1 || len(preferred.Prompts.Bundles) != 1 || preferred.Prompts.AllowsSelectedProfileInjection() {
		t.Fatalf("preferred playbook gate did not preserve runtime restriction: %+v %v", preferred, err)
	}
	legacy, err := svc.Load(context.Background(), "legacy")
	if err != nil || len(legacy.Prompts.Bundles) != 1 || legacy.Playbooks.Bundles != nil {
		t.Fatalf("legacy intent allow-list was not retained: %+v %v", legacy, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "conflict.yaml"), []byte("id: conflict\nplaybooks: {bundles: [one]}\nprompts: {bundles: [two]}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Load(context.Background(), "conflict"); err == nil || !strings.Contains(err.Error(), "duplicate agent playbook") {
		t.Fatalf("conflicting allow-list aliases accepted: %v", err)
	}
}

func TestAgentMarkdownKindMustMatch(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "agents"), 0700))
	file := filepath.Join(root, "agents", "named.md")
	require.NoError(t, os.WriteFile(file, []byte("---\nkind: agent\nid: named\n---\nbody"), 0600))
	svc := New(WithMetaService(meta.New(afs.New(), root)))
	_, err := svc.Load(context.Background(), "named")
	if err != nil {
		t.Fatal(err)
	}
	require.NoError(t, os.WriteFile(file, []byte("---\nkind: playbook\nid: named\n---\nbody"), 0600))
	if _, err := svc.Load(context.Background(), "named"); err == nil || !strings.Contains(err.Error(), "expected agent kind") {
		t.Fatalf("wrong frontmatter kind accepted: %v", err)
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/workspace"
)

type queryClientStub struct {
	got *agentsvc.QueryInput
	out *agentsvc.QueryOutput
	err error
}

func (s *queryClientStub) Query(_ context.Context, input *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
	s.got = input
	return s.out, s.err
}

func writeTestCLIWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "skills", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "skills", "demo", "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("default:\n  agent: coder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agentYAML := "id: coder\nname: Coder\nmodelRef: test-model\nskills:\n  - demo\n"
	if err := os.WriteFile(filepath.Join(root, "agents", "coder.yaml"), []byte(agentYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	skillMD := "---\nname: demo\ndescription: Demo skill.\n---\n\n# Demo Skill\nFollow the demo instructions.\n"
	if err := os.WriteFile(filepath.Join(root, "skills", "demo", "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "demo", "references", "guide.md"), []byte("# Guide\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func writePreprocessCLIWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "skills", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("default:\n  agent: coder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agentYAML := "id: coder\nname: Coder\nmodelRef: test-model\nskills:\n  - demo\n"
	if err := os.WriteFile(filepath.Join(root, "agents", "coder.yaml"), []byte(agentYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	skillMD := "---\nname: demo\ndescription: Demo skill.\npreprocess: true\npreprocess-timeout: 7\nallowed-tools: Bash(echo:*) system/exec:execute\n---\n\nBefore\n`!`echo hi`\nAfter\n"
	if err := os.WriteFile(filepath.Join(root, "skills", "demo", "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRunSkillListActivateDiagnostics(t *testing.T) {
	root := writeTestCLIWorkspace(t)
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)

	var out bytes.Buffer
	if err := run([]string{"skill", "list", "--workspace", root}, &out, &out); err != nil {
		t.Fatalf("skill list error: %v", err)
	}
	if !strings.Contains(out.String(), "demo\tDemo skill.") {
		t.Fatalf("unexpected list output: %q", out.String())
	}

	out.Reset()
	if err := run([]string{"skill", "activate", "--workspace", root, "demo", "arg1"}, &out, &out); err != nil {
		t.Fatalf("skill activate error: %v", err)
	}
	if !strings.Contains(out.String(), `Loaded skill "demo"`) {
		t.Fatalf("unexpected activate output: %q", out.String())
	}

	out.Reset()
	if err := run([]string{"skill", "diagnostics", "--workspace", root}, &out, &out); err != nil {
		t.Fatalf("skill diagnostics error: %v", err)
	}

	out.Reset()
	if err := run([]string{"skill", "show", "--workspace", root, "demo"}, &out, &out); err != nil {
		t.Fatalf("skill show error: %v", err)
	}
	if !strings.Contains(out.String(), "name:\tdemo") || !strings.Contains(out.String(), "# Demo Skill") {
		t.Fatalf("unexpected show output: %q", out.String())
	}
	if !strings.Contains(out.String(), "references:\treferences/guide.md") {
		t.Fatalf("expected references in show output: %q", out.String())
	}

	out.Reset()
	if err := run([]string{"skill", "show", "--workspace", root, "--path", "demo"}, &out, &out); err != nil {
		t.Fatalf("skill show --path error: %v", err)
	}
	if !strings.Contains(out.String(), filepath.Join(root, "skills", "demo", "SKILL.md")) {
		t.Fatalf("unexpected show path output: %q", out.String())
	}

	out.Reset()
	if err := run([]string{"skill", "show", "--workspace", root, "--json", "demo"}, &out, &out); err != nil {
		t.Fatalf("skill show --json error: %v", err)
	}
	var showJSON map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &showJSON); err != nil {
		t.Fatalf("unmarshal show json: %v", err)
	}
	if showJSON["name"] != "demo" {
		t.Fatalf("unexpected show json: %#v", showJSON)
	}
	refs, ok := showJSON["references"].([]interface{})
	if !ok || len(refs) != 1 || refs[0] != "references/guide.md" {
		t.Fatalf("unexpected show json references: %#v", showJSON)
	}

	out.Reset()
	if err := run([]string{"skill", "validate", filepath.Join(root, "skills", "demo")}, &out, &out); err != nil {
		t.Fatalf("skill validate error: %v", err)
	}
	if !strings.Contains(out.String(), "OK:") {
		t.Fatalf("unexpected validate output: %q", out.String())
	}

	strictDir := filepath.Join(root, "skills", "strictdemo")
	if err := os.MkdirAll(strictDir, 0o755); err != nil {
		t.Fatal(err)
	}
	strictSkill := "---\nname: strictdemo\ndescription: Strict skill.\nextra-field: value\n---\n\nBody\n"
	if err := os.WriteFile(filepath.Join(strictDir, "SKILL.md"), []byte(strictSkill), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"skill", "validate", "--strict", strictDir}, &out, &out); err != nil {
		coded, ok := err.(exitCoder)
		if !ok {
			t.Fatalf("expected exitCoder, got %T: %v", err, err)
		}
		if coded.ExitCode() != 2 {
			t.Fatalf("expected exit code 2, got %d", coded.ExitCode())
		}
	}
	if !strings.Contains(out.String(), "WARN: unknown frontmatter field") {
		t.Fatalf("unexpected strict validate output: %q", out.String())
	}
}

func TestRunQuery(t *testing.T) {
	root := writeTestCLIWorkspace(t)
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)

	stub := &queryClientStub{out: &agentsvc.QueryOutput{ConversationID: "c1", Content: "done"}}
	prev := buildQueryClient
	buildQueryClient = func(_ context.Context, workspaceRoot string) (queryClient, error) {
		if workspaceRoot != root {
			t.Fatalf("unexpected workspace root: %q", workspaceRoot)
		}
		return stub, nil
	}
	defer func() { buildQueryClient = prev }()

	var out bytes.Buffer
	if err := run([]string{"query", "--workspace", root, "--conversation", "conv-1", "--model", "openai_gpt-5.4", "extract", "headlines"}, &out, &out); err != nil {
		t.Fatalf("query error: %v", err)
	}
	if stub.got == nil {
		t.Fatal("expected query input")
	}
	if stub.got.AgentID != "coder" {
		t.Fatalf("expected default agent coder, got %q", stub.got.AgentID)
	}
	if stub.got.ConversationID != "conv-1" {
		t.Fatalf("expected conversation conv-1, got %q", stub.got.ConversationID)
	}
	if stub.got.ModelOverride != "openai_gpt-5.4" {
		t.Fatalf("expected model override openai_gpt-5.4, got %q", stub.got.ModelOverride)
	}
	if stub.got.Query != "extract headlines" || stub.got.DisplayQuery != "extract headlines" {
		t.Fatalf("unexpected query input: %#v", stub.got)
	}
	if !strings.Contains(out.String(), "done") {
		t.Fatalf("unexpected query output: %q", out.String())
	}
}

func TestRunSkillActivate_PreprocessesBody(t *testing.T) {
	root := writePreprocessCLIWorkspace(t)
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)

	var out bytes.Buffer
	if err := run([]string{"skill", "activate", "--workspace", root, "--agent", "coder", "demo"}, &out, &out); err != nil {
		t.Fatalf("skill activate error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Before\nhi\nAfter") {
		t.Fatalf("expected preprocessed body, got %q", got)
	}
	if strings.Contains(got, "`!`echo hi`") {
		t.Fatalf("expected raw preprocess placeholder removed, got %q", got)
	}
	if strings.Contains(got, "preprocess: disabled (no executor injected") {
		t.Fatalf("expected runtime-backed preprocessing, got %q", got)
	}
}

func TestRunSkillValidateAndShow_ContextExecutionModes(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "skills", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("default:\n  agent: coder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agentYAML := "id: coder\nname: Coder\nmodelRef: test-model\nskills:\n  - demo\n"
	if err := os.WriteFile(filepath.Join(root, "agents", "coder.yaml"), []byte(agentYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	skillMD := "---\nname: demo\ndescription: Demo skill.\ncontext: detach\n---\n\n# Demo Skill\nDetached flow.\n"
	if err := os.WriteFile(filepath.Join(root, "skills", "demo", "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTLY_WORKSPACE", root)
	workspace.SetRoot(root)

	var out bytes.Buffer
	if err := run([]string{"skill", "validate", filepath.Join(root, "skills", "demo")}, &out, &out); err != nil {
		t.Fatalf("skill validate error: %v", err)
	}
	if !strings.Contains(out.String(), "OK:") {
		t.Fatalf("unexpected validate output: %q", out.String())
	}

	out.Reset()
	if err := run([]string{"skill", "show", "--workspace", root, "--json", "demo"}, &out, &out); err != nil {
		t.Fatalf("skill show --json error: %v", err)
	}
	var showJSON map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &showJSON); err != nil {
		t.Fatalf("unmarshal show json: %v", err)
	}
	if showJSON["name"] != "demo" {
		t.Fatalf("unexpected show json: %#v", showJSON)
	}
	if showJSON["context"] != "detach" {
		t.Fatalf("expected context detach in show json, got %#v", showJSON)
	}
}

// TestSkillAdd_LocalPath_Workspace exercises the v1 install path end-to-end
// from a local directory into the workspace root. Verifies:
//   - the skill directory is copied into <workspace>/skills/<name>
//   - the SKILL.md content is preserved verbatim
//   - the _SOURCE provenance file is written
//   - --yes bypasses the confirmation prompt
//   - sub-resources (scripts/) copy recursively
//
// Persistence reuse: install just writes files; the watcher (when running)
// detects them via existing fsnotify infrastructure. The CLI doesn't emit
// registry-reload events — same contract per skill-impr.md S12.
func TestSkillAdd_LocalPath_Workspace(t *testing.T) {
	srcRoot := t.TempDir()
	srcSkill := filepath.Join(srcRoot, "demo")
	if err := os.MkdirAll(srcSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: demo\ndescription: Demo for skill add.\nlicense: Apache-2.0\n---\n\n# Demo Skill\n"
	if err := os.WriteFile(filepath.Join(srcSkill, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(srcSkill, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcSkill, "scripts", "hello.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	dstRoot := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", dstRoot)
	workspace.SetRoot(dstRoot)

	var out bytes.Buffer
	err := run([]string{"skill", "add", "--workspace", dstRoot, "--root", "workspace", "--yes", srcSkill}, &out, &out)
	if err != nil {
		t.Fatalf("skill add error: %v\noutput: %s", err, out.String())
	}

	installed := filepath.Join(dstRoot, "skills", "demo", "SKILL.md")
	gotBody, err := os.ReadFile(installed)
	if err != nil {
		t.Fatalf("installed SKILL.md missing: %v", err)
	}
	if string(gotBody) != body {
		t.Fatalf("body mismatch:\nwant: %q\ngot:  %q", body, string(gotBody))
	}

	provPath := filepath.Join(dstRoot, "skills", "demo", "_SOURCE")
	prov, err := os.ReadFile(provPath)
	if err != nil {
		t.Fatalf("_SOURCE missing: %v", err)
	}
	if !strings.Contains(string(prov), "name: demo") {
		t.Fatalf("_SOURCE missing name: %q", string(prov))
	}
	if !strings.Contains(string(prov), "origin: ") {
		t.Fatalf("_SOURCE missing origin: %q", string(prov))
	}

	if _, err := os.Stat(filepath.Join(dstRoot, "skills", "demo", "scripts", "hello.sh")); err != nil {
		t.Fatalf("recursive copy failed: %v", err)
	}

	if !strings.Contains(out.String(), "Installed") {
		t.Fatalf("expected 'Installed' in output, got: %s", out.String())
	}
}

// TestSkillAdd_RejectsInvalidSkill: install must validate via skillproto.Parse
// before touching the target tree. A skill with an empty name fails fast
// without any filesystem write.
func TestSkillAdd_RejectsInvalidSkill(t *testing.T) {
	srcRoot := t.TempDir()
	srcSkill := filepath.Join(srcRoot, "broken")
	if err := os.MkdirAll(srcSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\ndescription: no name\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(srcSkill, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	dstRoot := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", dstRoot)
	workspace.SetRoot(dstRoot)

	var out bytes.Buffer
	err := run([]string{"skill", "add", "--workspace", dstRoot, "--yes", srcSkill}, &out, &out)
	if err == nil {
		t.Fatalf("expected error on invalid skill, got nil; output: %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dstRoot, "skills", "broken")); err == nil {
		t.Fatalf("invalid skill should not write target tree")
	}
}

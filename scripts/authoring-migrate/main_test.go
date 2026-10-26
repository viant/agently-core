package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viant/agently-core/workspace/codec"
)

func TestConvertPlaybookPreservesValuesAndImportTargets(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	input := filepath.Join(source, "intents", "review.yaml")
	output := filepath.Join(target, "playbooks", "review.md")
	if err := os.MkdirAll(filepath.Dir(input), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, []byte("id: review\nunknownField:\n  enabled: true\nmessages:\n  - role: system\n    text: $import(scenarios/review.user.md)\n  - role: user\n    text: '  preserve whitespace  '\n"), 0600); err != nil {
		t.Fatal(err)
	}
	convert(input, output, source, target, true)
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := codec.ParseMarkdown(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Messages) != 2 || doc.Messages[0].Role != "system" || doc.Messages[0].Text != "$import(../intents/scenarios/review.user.md)" || doc.Messages[1].Text != "  preserve whitespace  " {
		t.Fatalf("message content or ordering changed: %+v", doc.Messages)
	}
	if !strings.Contains(string(doc.Metadata), "unknownField:") {
		t.Fatal("unrecognized frontmatter field was dropped")
	}
	if _, err := os.Stat(input); err != nil {
		t.Fatal("source must remain intact:", err)
	}
}

func TestConvertAgentKeepsPromptAliasesAndEngine(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	input := filepath.Join(source, "agents", "assistant", "assistant.yaml")
	output := filepath.Join(target, "agents", "assistant", "assistant.md")
	if err := os.MkdirAll(filepath.Dir(input), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, []byte("id: assistant\nprompt:\n  uri: prompt/user.tmpl\nsystemPrompt:\n  uri: prompt/system.tmpl\n  engine: go\ninstruction:\n  uri: prompt/instruction.tmpl\ncustomValue: enabled\n"), 0600); err != nil {
		t.Fatal(err)
	}
	convert(input, output, source, target, false)
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := codec.ParseMarkdown(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Messages) != 2 || doc.Messages[0].Role != "system" || doc.Messages[0].Field != "systemPrompt" || doc.Messages[0].Engine != "go" || doc.Messages[1].Role != "user" || doc.Messages[1].Field != "prompt" || !strings.Contains(string(doc.Metadata), "instruction:") || !strings.Contains(string(doc.Metadata), "customValue: enabled") {
		t.Fatalf("agent metadata drifted: %s", raw)
	}
}

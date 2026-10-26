// Command authoring-inspect exposes authored workspace definitions as JSON for
// structural contract tests. Markdown is parsed by the production Forge codec;
// the Core loader parity gate separately verifies effective runtime models.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/viant/agently-core/workspace/codec"
	"gopkg.in/yaml.v3"
)

func main() {
	workspace := flag.String("workspace", "", "workspace root")
	flag.Parse()
	if *workspace == "" {
		fail("-workspace is required")
	}
	root, err := filepath.Abs(*workspace)
	check(err)
	result := map[string]map[string]any{"playbooks": {}, "agents": {}}
	intents, err := filepath.Glob(filepath.Join(root, "intents", "*.yaml"))
	check(err)
	playbooks, err := filepath.Glob(filepath.Join(root, "playbooks", "*.md"))
	check(err)
	for _, file := range append(intents, playbooks...) {
		id := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		if _, present := result["playbooks"][id]; present {
			fail("duplicate playbook identity %s", id)
		}
		result["playbooks"][id] = readDefinition(file, false)
	}
	agents, err := filepath.Glob(filepath.Join(root, "agents", "*", "*.yaml"))
	check(err)
	markdownAgents, err := filepath.Glob(filepath.Join(root, "agents", "*", "*.md"))
	check(err)
	for _, file := range append(agents, markdownAgents...) {
		id := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		if id != filepath.Base(filepath.Dir(file)) {
			continue // supporting Markdown under an agent directory
		}
		if _, present := result["agents"][id]; present {
			fail("duplicate agent identity %s", id)
		}
		result["agents"][id] = readDefinition(file, true)
	}
	encoded, err := json.Marshal(result)
	check(err)
	_, err = os.Stdout.Write(encoded)
	check(err)
}

func readDefinition(file string, agent bool) map[string]any {
	raw, err := os.ReadFile(file)
	check(err)
	if filepath.Ext(file) == ".yaml" {
		return decodeYAML(raw)
	}
	document, err := codec.ParseMarkdown(raw)
	check(err)
	definition := decodeYAML(document.Metadata)
	if agent {
		for _, message := range document.Messages {
			var slot string
			switch {
			case message.Role == "system" && message.Field == "systemPrompt":
				slot = "systemPrompt"
			case message.Role == "system" && message.Field == "instructionPrompt":
				slot = "instructionPrompt"
			case message.Role == "user" && (message.Field == "" || message.Field == "prompt"):
				slot = "prompt"
			default:
				fail("%s: unsupported agent message role/field", file)
			}
			if _, exists := definition[slot]; exists {
				fail("%s: duplicate agent prompt slot %s", file, slot)
			}
			prompt := map[string]any{}
			if message.URI != "" {
				prompt["uri"] = message.URI
			}
			if message.Text != "" {
				prompt["text"] = message.Text
			}
			if message.Engine != "" {
				prompt["engine"] = message.Engine
			}
			definition[slot] = prompt
		}
		return definition
	}
	if len(document.Messages) > 0 {
		if _, exists := definition["messages"]; exists {
			fail("%s: duplicate playbook messages", file)
		}
		messages := make([]map[string]any, 0, len(document.Messages))
		for _, message := range document.Messages {
			if message.Field != "" || message.Engine != "" {
				fail("%s: unsupported playbook message field/engine", file)
			}
			item := map[string]any{"role": message.Role}
			if message.Text != "" {
				item["text"] = message.Text
			}
			if message.URI != "" {
				item["uri"] = message.URI
			}
			messages = append(messages, item)
		}
		definition["messages"] = messages
	}
	return definition
}

func decodeYAML(raw []byte) map[string]any {
	var value map[string]any
	check(yaml.Unmarshal(raw, &value))
	if value == nil {
		return map[string]any{}
	}
	return value
}

func check(err error) {
	if err != nil {
		fail("%v", err)
	}
}
func fail(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...); os.Exit(1) }

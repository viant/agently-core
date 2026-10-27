// Command authoring-migrate prepares a loss-checked workspace YAML to Markdown
// candidate. It never removes or edits the source definitions.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/viant/afs"
	agentloader "github.com/viant/agently-core/protocol/agent/loader"
	"github.com/viant/agently-core/workspace/codec"
	intakerepo "github.com/viant/agently-core/workspace/repository/intake"
	meta "github.com/viant/agently-core/workspace/service/meta"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
	"gopkg.in/yaml.v3"
)

var importPattern = regexp.MustCompile(`\$import\(([^)]+)\)`)

func main() {
	workspace := flag.String("workspace", "", "workspace containing intents and agents")
	output := flag.String("out", "", "separate candidate workspace directory")
	flag.Parse()
	if *workspace == "" || *output == "" {
		fatalf("both -workspace and -out are required")
	}
	source, err := filepath.Abs(*workspace)
	check(err)
	target, err := filepath.Abs(*output)
	check(err)
	if target == source || strings.HasPrefix(target, source+string(filepath.Separator)) {
		fatalf("output must be outside the source workspace")
	}
	if _, err := os.Stat(target); err == nil {
		fatalf("output already exists: %s", target)
	} else if !os.IsNotExist(err) {
		check(err)
	}
	intents, err := filepath.Glob(filepath.Join(source, "intents", "*.yaml"))
	check(err)
	agents, err := filepath.Glob(filepath.Join(source, "agents", "*", "*.yaml"))
	check(err)
	if len(intents) == 0 || len(agents) == 0 {
		fatalf("workspace must contain intent and agent definitions")
	}
	for _, input := range intents {
		name := strings.TrimSuffix(filepath.Base(input), ".yaml") + ".md"
		convert(input, filepath.Join(target, "playbooks", name), source, target, true)
	}
	for _, input := range agents {
		relative, err := filepath.Rel(source, input)
		check(err)
		convert(input, filepath.Join(target, strings.TrimSuffix(relative, ".yaml")+".md"), source, target, false)
	}
	check(linkSupportingFiles(source, target))
	check(verifyRuntimeModels(source, target, intents, agents))
	fmt.Printf("validated candidates: intents=%d agents=%d at %s\n", len(intents), len(agents), target)
}

// Candidate files live outside the source tree. Supporting links let the
// normal loaders resolve the exact same imported files and agent prompt URIs.
func linkSupportingFiles(source, target string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		switch entry.Name() {
		case "intents", "agents", "playbooks":
			continue
		}
		if err := os.Symlink(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(target, "intents"), 0700); err != nil {
		return err
	}
	if err := os.Symlink(filepath.Join(source, "intents", "scenarios"), filepath.Join(target, "intents", "scenarios")); err != nil {
		return err
	}
	agentDirs, err := os.ReadDir(filepath.Join(source, "agents"))
	if err != nil {
		return err
	}
	for _, agentDir := range agentDirs {
		if !agentDir.IsDir() {
			continue
		}
		fromDir := filepath.Join(source, "agents", agentDir.Name())
		toDir := filepath.Join(target, "agents", agentDir.Name())
		if err := os.MkdirAll(toDir, 0700); err != nil {
			return err
		}
		files, err := os.ReadDir(fromDir)
		if err != nil {
			return err
		}
		for _, file := range files {
			if file.Name() == agentDir.Name()+".yaml" {
				continue
			}
			if err := os.Symlink(filepath.Join(fromDir, file.Name()), filepath.Join(toDir, file.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func verifyRuntimeModels(source, target string, intents, agents []string) error {
	ctx := context.Background()
	oldIntake := intakerepo.NewWithStore(fsstore.New(source))
	newIntake := intakerepo.NewWithStore(fsstore.New(target))
	oldAll, err := oldIntake.LoadAll(ctx)
	if err != nil {
		return fmt.Errorf("load all YAML playbooks: %w", err)
	}
	newAll, err := newIntake.LoadAll(ctx)
	if err != nil {
		return fmt.Errorf("load all Markdown playbooks: %w", err)
	}
	if len(oldAll) != len(intents) || len(newAll) != len(intents) {
		return fmt.Errorf("playbook catalog count differs: old=%d new=%d expected=%d", len(oldAll), len(newAll), len(intents))
	}
	for _, filename := range intents {
		name := strings.TrimSuffix(filepath.Base(filename), ".yaml")
		before, err := oldIntake.Load(ctx, name)
		if err != nil {
			return fmt.Errorf("load YAML playbook %s: %w", name, err)
		}
		after, err := newIntake.Load(ctx, name)
		if err != nil {
			return fmt.Errorf("load Markdown playbook %s: %w", name, err)
		}
		if !reflect.DeepEqual(before, after) {
			return fmt.Errorf("playbook runtime model differs: %s", name)
		}
	}
	oldAgents := agentloader.New(agentloader.WithMetaService(meta.New(afs.New(), source)))
	newAgents := agentloader.New(agentloader.WithMetaService(meta.New(afs.New(), target)))
	oldAgentList, err := oldAgents.LoadAgents(ctx, filepath.Join(source, "agents"))
	if err != nil {
		return fmt.Errorf("load all YAML agents: %w", err)
	}
	newAgentList, err := newAgents.LoadAgents(ctx, filepath.Join(target, "agents"))
	if err != nil {
		return fmt.Errorf("load all Markdown agents: %w", err)
	}
	if len(oldAgentList) != len(agents) || len(newAgentList) != len(agents) {
		return fmt.Errorf("agent catalog count differs: old=%d new=%d expected=%d", len(oldAgentList), len(newAgentList), len(agents))
	}
	for _, filename := range agents {
		relative, err := filepath.Rel(source, filename)
		if err != nil {
			return err
		}
		before, err := oldAgents.Load(ctx, filename)
		if err != nil {
			return fmt.Errorf("load YAML agent %s: %w", relative, err)
		}
		after, err := newAgents.Load(ctx, filepath.Join(target, strings.TrimSuffix(relative, ".yaml")+".md"))
		if err != nil {
			return fmt.Errorf("load Markdown agent %s: %w", relative, err)
		}
		before.Source, after.Source = nil, nil // authored location intentionally changes
		oldJSON, err := json.Marshal(before)
		if err != nil {
			return err
		}
		newJSON, err := json.Marshal(after)
		if err != nil {
			return err
		}
		newJSON = []byte(strings.ReplaceAll(string(newJSON), target, source))
		if !reflect.DeepEqual(oldJSON, newJSON) {
			return fmt.Errorf("agent runtime model differs: %s (first mismatch %s)", relative, firstJSONDifference(oldJSON, newJSON))
		}
	}
	return nil
}

func firstJSONDifference(left, right []byte) string {
	var a, b any
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return "invalid JSON"
	}
	var walk func(any, any, string) string
	walk = func(a, b any, path string) string {
		if reflect.DeepEqual(a, b) {
			return ""
		}
		am, aok := a.(map[string]any)
		bm, bok := b.(map[string]any)
		if aok && bok {
			for key, av := range am {
				bv, ok := bm[key]
				if !ok {
					return path + "." + key + " missing"
				}
				if difference := walk(av, bv, path+"."+key); difference != "" {
					return difference
				}
			}
			for key := range bm {
				if _, ok := am[key]; !ok {
					return path + "." + key + " added"
				}
			}
		}
		return path
	}
	return walk(a, b, "agent")
}

func convert(input, output, source, target string, playbook bool) {
	raw, err := os.ReadFile(input)
	check(err)
	var original yaml.Node
	check(yaml.Unmarshal(raw, &original))
	if len(original.Content) != 1 || original.Content[0].Kind != yaml.MappingNode {
		fatalf("%s: expected one YAML mapping", input)
	}
	// Keep every other authored field and comment in frontmatter. Agent's
	// legacy `instruction` alias stays there; user/system prompts become
	// explicit role sections with their URI and Engine intact.
	metadata := cloneNode(&original)
	var messages []codec.MarkdownMessage
	if playbook {
		entry := popMapping(metadata.Content[0], "messages")
		if entry != nil {
			if entry.Kind != yaml.SequenceNode {
				fatalf("%s: messages must be a sequence", input)
			}
			for _, item := range entry.Content {
				var message struct {
					Role string `yaml:"role"`
					Text string `yaml:"text"`
					URI  string `yaml:"uri"`
				}
				check(item.Decode(&message))
				if item.Kind != yaml.MappingNode || len(item.Content) != 2*nonemptyMessageFields(message) {
					fatalf("%s: unsupported message field or empty field", input)
				}
				message.Text = relocateImports(message.Text, input, output, source, target)
				messages = append(messages, codec.MarkdownMessage{Role: message.Role, Text: message.Text, URI: message.URI})
			}
		}
	} else {
		for _, slot := range []struct{ key, role, field string }{
			{"systemPrompt", "system", "systemPrompt"},
			{"prompt", "user", "prompt"},
		} {
			entry := popMapping(metadata.Content[0], slot.key)
			if entry == nil {
				continue
			}
			var prompt struct {
				URI    string `yaml:"uri"`
				Text   string `yaml:"text"`
				Engine string `yaml:"engine"`
			}
			check(entry.Decode(&prompt))
			if entry.Kind != yaml.MappingNode || len(entry.Content) != 2*nonemptyPromptFields(prompt) {
				fatalf("%s: unsupported %s field", input, slot.key)
			}
			messages = append(messages, codec.MarkdownMessage{Role: slot.role, Field: slot.field, URI: prompt.URI, Text: prompt.Text, Engine: prompt.Engine})
		}
	}
	frontmatter, err := yaml.Marshal(metadata.Content[0])
	check(err)
	candidate, err := codec.MarshalMarkdown(frontmatter, messages)
	check(err)
	parsed, err := codec.ParseMarkdown(candidate)
	check(err)
	var reconstructed yaml.Node
	check(yaml.Unmarshal(parsed.Metadata, &reconstructed))
	if playbook && len(parsed.Messages) > 0 {
		sequence := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, message := range parsed.Messages {
			item := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			appendScalar(item, "role", message.Role)
			if message.Text != "" {
				appendScalar(item, "text", relocateImports(message.Text, output, input, target, source))
			}
			if message.URI != "" {
				appendScalar(item, "uri", message.URI)
			}
			sequence.Content = append(sequence.Content, item)
		}
		appendMapping(reconstructed.Content[0], "messages", sequence)
	} else if !playbook {
		for _, message := range parsed.Messages {
			var key string
			switch {
			case message.Role == "system" && message.Field == "systemPrompt":
				key = "systemPrompt"
			case message.Role == "user" && message.Field == "prompt":
				key = "prompt"
			default:
				fatalf("%s: unexpected agent prompt section", input)
			}
			item := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			if message.URI != "" {
				appendScalar(item, "uri", message.URI)
			}
			if message.Text != "" {
				appendScalar(item, "text", message.Text)
			}
			if message.Engine != "" {
				appendScalar(item, "engine", message.Engine)
			}
			appendMapping(reconstructed.Content[0], key, item)
		}
	}
	if !equalYAML(&original, &reconstructed) {
		fatalf("%s: candidate semantic round trip differs", input)
	}
	check(os.MkdirAll(filepath.Dir(output), 0700))
	check(os.WriteFile(output, candidate, 0600))
}

func relocateImports(text, from, to, source, target string) string {
	return importPattern.ReplaceAllStringFunc(text, func(match string) string {
		path := strings.TrimSuffix(strings.TrimPrefix(match, "$import("), ")")
		if filepath.IsAbs(path) || strings.Contains(path, "://") {
			return match
		}
		resolved := filepath.Clean(filepath.Join(filepath.Dir(from), filepath.FromSlash(path)))
		if strings.HasPrefix(resolved, source+string(filepath.Separator)) {
			relative, err := filepath.Rel(source, resolved)
			check(err)
			resolved = filepath.Join(target, relative)
		}
		relative, err := filepath.Rel(filepath.Dir(to), resolved)
		check(err)
		return "$import(" + filepath.ToSlash(relative) + ")"
	})
}

func nonemptyMessageFields(value struct {
	Role string `yaml:"role"`
	Text string `yaml:"text"`
	URI  string `yaml:"uri"`
}) int {
	count := 1
	if value.Text != "" {
		count++
	}
	if value.URI != "" {
		count++
	}
	return count
}

func nonemptyPromptFields(value struct {
	URI    string `yaml:"uri"`
	Text   string `yaml:"text"`
	Engine string `yaml:"engine"`
}) int {
	count := 0
	if value.URI != "" {
		count++
	}
	if value.Text != "" {
		count++
	}
	if value.Engine != "" {
		count++
	}
	return count
}

func equalYAML(left, right *yaml.Node) bool {
	var a, b any
	if left.Decode(&a) != nil || right.Decode(&b) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}

func popMapping(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			value := node.Content[i+1]
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return value
		}
	}
	return nil
}

func appendMapping(node *yaml.Node, key string, value *yaml.Node) {
	node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func appendScalar(node *yaml.Node, key, value string) {
	appendMapping(node, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

func cloneNode(node *yaml.Node) *yaml.Node {
	copy := *node
	copy.Content = make([]*yaml.Node, len(node.Content))
	for index, item := range node.Content {
		copy.Content[index] = cloneNode(item)
	}
	return &copy
}

func check(err error) {
	if err != nil {
		fatalf("%v", err)
	}
}
func fatalf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...); os.Exit(1) }

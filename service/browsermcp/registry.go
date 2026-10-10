// Package browsermcp binds ephemeral browser MCP catalogs to authenticated
// conversation owners. Catalog metadata never grants native tool authority.
package browsermcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/agui"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcpname"
	"github.com/viant/agently-core/runtime/mcpapps"
)

const Prefix = "browser_mcp_"

var ErrUnavailable = errors.New("browser MCP catalog unavailable for this conversation")

type Tool struct {
	Meta        json.RawMessage `json:"_meta,omitempty"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}
type Registration struct {
	ConversationID string `json:"conversationId"`
	ThreadID       string `json:"threadId"`
	Server         string `json:"server"`
	ConnectionID   string `json:"connectionId"`
	Tools          []Tool `json:"tools"`
	Resources      bool   `json:"resources,omitempty"`
}
type Catalog struct {
	ID             string      `json:"id"`
	Hash           string      `json:"hash"`
	ConnectionID   string      `json:"connectionId"`
	ConversationID string      `json:"conversationId"`
	ThreadID       string      `json:"threadId"`
	Server         string      `json:"server"`
	Tools          []agui.Tool `json:"tools"`
	Resources      bool        `json:"resources,omitempty"`
	ExpiresAt      time.Time   `json:"expiresAt"`
}
type catalogEntry struct {
	owner      string
	configHash string
	catalog    Catalog
}
type Registry struct {
	mu       sync.Mutex
	configs  func(context.Context) ([]cfg.BrowserDescriptor, error)
	catalogs map[string]catalogEntry
}

func New(loader func(context.Context) ([]cfg.BrowserDescriptor, error)) *Registry {
	return &Registry{configs: loader, catalogs: map[string]catalogEntry{}}
}
func (r *Registry) Descriptors(ctx context.Context) ([]cfg.BrowserDescriptor, error) {
	if r == nil || r.configs == nil {
		return nil, ErrUnavailable
	}
	return r.configs(ctx)
}
func hash(v any) string {
	raw, _ := json.Marshal(v)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func alias(server, name string) string {
	prefix := mcpname.NewName(server, "").String()
	if regexp.MustCompile(`^[A-Za-z0-9_]+$`).MatchString(name) && len(prefix)+len(name) <= 64 {
		return prefix + name
	}
	return prefix + "tool_" + hash(name)[:16]
}
func (r *Registry) Register(ctx context.Context, user string, in Registration) (Catalog, error) {
	if user == "" || in.ThreadID == "" || in.ConversationID == "" || !regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`).MatchString(in.ConnectionID) || len(in.Tools) == 0 || len(in.Tools) > 64 {
		return Catalog{}, ErrUnavailable
	}
	configs, err := r.Descriptors(ctx)
	if err != nil {
		return Catalog{}, ErrUnavailable
	}
	var config *cfg.BrowserDescriptor
	for _, candidate := range configs {
		if candidate.Name == in.Server {
			v := candidate
			config = &v
			break
		}
	}
	if config == nil {
		return Catalog{}, ErrUnavailable
	}
	tools := make([]agui.Tool, 0, len(in.Tools))
	seen := map[string]bool{}
	random := make([]byte, 24)
	if _, err = rand.Read(random); err != nil {
		return Catalog{}, ErrUnavailable
	}
	id := hex.EncodeToString(random)
	for _, tool := range in.Tools {
		if len(tool.Name) == 0 || len(tool.Name) > 128 || len(tool.Description) > 8192 || len(tool.InputSchema) > 49152 || seen[tool.Name] {
			return Catalog{}, ErrUnavailable
		}
		seen[tool.Name] = true
		allowed := false
		for _, pattern := range config.AllowedTools {
			if match, _ := path.Match(pattern, tool.Name); match {
				allowed = true
			}
		}
		if !allowed {
			return Catalog{}, ErrUnavailable
		}
		var input map[string]any
		if json.Unmarshal(tool.InputSchema, &input) != nil || input["type"] != "object" {
			return Catalog{}, ErrUnavailable
		}
		budget := 4096
		if !boundedSchema(input, 0, &budget) {
			return Catalog{}, ErrUnavailable
		}
		parameters, _ := json.Marshal(input)
		metadata := map[string]any{"browserMCP": map[string]any{"catalogId": id, "connectionId": in.ConnectionID, "server": in.Server, "tool": tool.Name}}
		if len(tool.Meta) > 0 {
			if len(tool.Meta) > 8192 {
				return Catalog{}, ErrUnavailable
			}
			var rawMeta map[string]interface{}
			if json.Unmarshal(tool.Meta, &rawMeta) != nil {
				return Catalog{}, ErrUnavailable
			}
			if rawUI, exists := rawMeta["ui"]; exists {
				ui, ok := rawUI.(map[string]interface{})
				if !ok {
					return Catalog{}, ErrUnavailable
				}
				safe := map[string]interface{}{}
				if uri, exists := ui["resourceUri"]; exists {
					value, ok := uri.(string)
					if !ok || !strings.HasPrefix(value, "ui://") || len(value) > 2048 {
						return Catalog{}, ErrUnavailable
					}
					safe["resourceUri"] = value
				}
				if visibility, exists := ui["visibility"]; exists {
					// Validation is independent of the selected audience, including empty lists.
					values, ok := visibility.([]interface{})
					if !ok || len(values) > 2 {
						return Catalog{}, ErrUnavailable
					}
					for _, audience := range values {
						if audience != "model" && audience != "app" {
							return Catalog{}, ErrUnavailable
						}
					}
					safe["visibility"] = visibility
				}
				metadata["ui"] = safe
			}
		}
		meta, _ := json.Marshal(metadata)
		tools = append(tools, agui.Tool{Name: alias(in.Server, tool.Name), Description: "[" + in.Server + "/" + tool.Name + "] " + tool.Description, Parameters: parameters, Metadata: meta})
	}
	catalog := Catalog{ID: id, Hash: hash(tools), ConnectionID: in.ConnectionID, ConversationID: in.ConversationID, ThreadID: in.ThreadID, Server: in.Server, Tools: tools, Resources: in.Resources, ExpiresAt: time.Now().Add(15 * time.Minute)}
	raw, _ := json.Marshal(catalog)
	if len(raw) > 256*1024 {
		return Catalog{}, ErrUnavailable
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, e := range r.catalogs {
		if !e.catalog.ExpiresAt.After(time.Now()) || e.owner == user && e.catalog.ThreadID == in.ThreadID && e.catalog.Server == in.Server {
			delete(r.catalogs, key)
		}
	}
	count := 0
	for _, e := range r.catalogs {
		if e.owner == user {
			count++
		}
	}
	if count >= 32 || len(r.catalogs) >= 256 {
		return Catalog{}, ErrUnavailable
	}
	r.catalogs[id] = catalogEntry{owner: user, configHash: hash(config), catalog: catalog}
	var public Catalog
	_ = json.Unmarshal(raw, &public)
	return public, nil
}
func (r *Registry) Revoke(user, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.catalogs[id]
	if !ok || entry.owner != user {
		return ErrUnavailable
	}
	delete(r.catalogs, id)
	return nil
}

// Validate binds exact name/schema/metadata to the current authenticated user,
// original thread, configured server and registered device connection.
func (r *Registry) Validate(ctx context.Context, user, thread string, tools []agui.Tool) (map[string]llm.ToolDefinition, error) {
	verified := map[string]llm.ToolDefinition{}
	var configs []cfg.BrowserDescriptor
	for _, tool := range tools {
		var probe map[string]json.RawMessage
		_ = json.Unmarshal(tool.Metadata, &probe)
		hasBrowser := probe["browserMCP"] != nil
		if r != nil && configs == nil {
			var err error
			configs, err = r.Descriptors(ctx)
			if err != nil {
				return nil, ErrUnavailable
			}
		}
		configuredBrowser := false
		for _, config := range configs {
			if strings.HasPrefix(mcpname.Canonical(tool.Name), config.Name+"-") {
				configuredBrowser = true
			}
		}
		if !hasBrowser && !configuredBrowser {
			continue
		}
		if r == nil || !hasBrowser {
			return nil, ErrUnavailable
		}
		var meta struct {
			Browser struct {
				CatalogID string `json:"catalogId"`
			} `json:"browserMCP"`
		}
		if json.Unmarshal(tool.Metadata, &meta) != nil || meta.Browser.CatalogID == "" {
			return nil, ErrUnavailable
		}
		r.mu.Lock()
		entry, ok := r.catalogs[meta.Browser.CatalogID]
		r.mu.Unlock()
		if !ok || entry.owner != user || entry.catalog.ThreadID != thread || !entry.catalog.ExpiresAt.After(time.Now()) {
			return nil, ErrUnavailable
		}
		configCurrent := false
		for _, config := range configs {
			if config.Name == entry.catalog.Server && hash(config) == entry.configHash {
				configCurrent = true
			}
		}
		if !configCurrent {
			return nil, ErrUnavailable
		}
		matched := false
		for _, expected := range entry.catalog.Tools {
			if expected.Name != tool.Name {
				continue
			}
			var a, b map[string]any
			json.Unmarshal(expected.Parameters, &a)
			json.Unmarshal(tool.Parameters, &b)
			var am, bm map[string]any
			json.Unmarshal(expected.Metadata, &am)
			json.Unmarshal(tool.Metadata, &bm)
			if expected.Description != tool.Description || !reflect.DeepEqual(a, b) || !reflect.DeepEqual(am, bm) {
				return nil, ErrUnavailable
			}
			if !mcpapps.Visible(am, "model") {
				return nil, ErrUnavailable
			}
			verified[tool.Name] = llm.ToolDefinition{Name: tool.Name, Description: tool.Description, Parameters: a}
			matched = true
		}
		if !matched {
			return nil, ErrUnavailable
		}
	}
	return verified, nil
}

type definitionsKey struct{}

func WithDefinitions(ctx context.Context, defs map[string]llm.ToolDefinition) context.Context {
	return context.WithValue(ctx, definitionsKey{}, defs)
}
func Definitions(ctx context.Context) map[string]llm.ToolDefinition {
	result, _ := ctx.Value(definitionsKey{}).(map[string]llm.ToolDefinition)
	return result
}

func VerifyResultMetadata(name string, expected, actual json.RawMessage) error {
	var left, right map[string]any
	_ = json.Unmarshal(expected, &left)
	_ = json.Unmarshal(actual, &right)
	if left["browserMCP"] == nil && right["browserMCP"] == nil {
		return nil
	}
	if left["browserMCP"] == nil || !reflect.DeepEqual(left, right) {
		return ErrUnavailable
	}
	return nil
}

func (r *Registry) Current(ctx context.Context, user, id string) (Catalog, error) {
	r.mu.Lock()
	entry, ok := r.catalogs[id]
	r.mu.Unlock()
	if !ok || entry.owner != user || !entry.catalog.ExpiresAt.After(time.Now()) {
		return Catalog{}, ErrUnavailable
	}
	configs, err := r.Descriptors(ctx)
	if err != nil {
		return Catalog{}, ErrUnavailable
	}
	for _, config := range configs {
		if config.Name == entry.catalog.Server && hash(config) == entry.configHash {
			raw, _ := json.Marshal(entry.catalog)
			var result Catalog
			_ = json.Unmarshal(raw, &result)
			return result, nil
		}
	}
	return Catalog{}, ErrUnavailable
}

func boundedSchema(value any, depth int, budget *int) bool {
	*budget = *budget - 1
	if depth > 48 || *budget < 0 {
		return false
	}
	switch item := value.(type) {
	case map[string]any:
		if len(item) > 256 {
			return false
		}
		for key, child := range item {
			if key == "$ref" || key == "$dynamicRef" {
				reference, ok := child.(string)
				if !ok || !strings.HasPrefix(reference, "#") {
					return false
				}
			}
			if key == "$schema" {
				schema, ok := child.(string)
				if !ok || schema != "https://json-schema.org/draft/2020-12/schema" && schema != "https://json-schema.org/draft/2019-09/schema" && schema != "http://json-schema.org/draft-07/schema#" && schema != "http://json-schema.org/draft-04/schema#" {
					return false
				}
			}
			if !boundedSchema(child, depth+1, budget) {
				return false
			}
		}
	case []any:
		if len(item) > 256 {
			return false
		}
		for _, child := range item {
			if !boundedSchema(child, depth+1, budget) {
				return false
			}
		}
	case string:
		return len(item) <= 16384
	}
	return true
}

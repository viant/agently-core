package skill

import (
	"context"
	"encoding/json"
	"fmt"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	schema "github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
)

// toolSkillBridge adapts metadata operations only. File reads still go through
// resources/read, and retain manifest validation and origin enforcement.
type toolSkillBridge struct {
	client.Interface
	tools cfg.SkillToolBridge
}

const (
	defaultSkillListTool = "skills/list"
	defaultSkillGetTool  = "skills/get"
)

// resolveSkillToolBridge prefers an operator mapping, then recognizes the
// conventional compatibility tools advertised by older MCP servers. Discovery
// remains request-scoped and uses the caller's authorization options.
func resolveSkillToolBridge(ctx context.Context, cli client.Interface, server *cfg.MCPClient, opts []client.RequestOption) (cfg.SkillToolBridge, error) {
	if server != nil && server.SkillDiscovery != nil && server.SkillDiscovery.Tools != nil {
		configured := *server.SkillDiscovery.Tools
		if configured.List != "" && configured.Get != "" {
			return configured, nil
		}
	}
	listed, err := cli.ListTools(ctx, nil, opts...)
	if err != nil {
		return cfg.SkillToolBridge{}, fmt.Errorf("skills extension not declared and fallback tool discovery failed: %w", err)
	}
	var hasList, hasGet bool
	if listed != nil {
		for _, item := range listed.Tools {
			switch item.Name {
			case defaultSkillListTool:
				hasList = true
			case defaultSkillGetTool:
				hasGet = true
			}
		}
	}
	if !hasList || !hasGet {
		return cfg.SkillToolBridge{}, fmt.Errorf("skills extension not declared and fallback tools are unavailable")
	}
	return cfg.SkillToolBridge{List: defaultSkillListTool, Get: defaultSkillGetTool}, nil
}

func (b *toolSkillBridge) call(ctx context.Context, name string, args map[string]interface{}, out interface{}, opts ...client.RequestOption) error {
	result, err := b.CallTool(ctx, &schema.CallToolRequestParams{Name: name, Arguments: args}, opts...)
	if err != nil {
		return err
	}
	if result == nil || (result.IsError != nil && *result.IsError) {
		return fmt.Errorf("legacy skill metadata tool failed")
	}
	var raw []byte
	if result.StructuredContent != nil {
		raw, err = json.Marshal(result.StructuredContent)
	} else {
		if len(result.Content) != 1 {
			return fmt.Errorf("legacy skill metadata must be one JSON object")
		}
		encoded, e := json.Marshal(result.Content[0])
		if e != nil {
			return e
		}
		var text struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if e = json.Unmarshal(encoded, &text); e != nil {
			return e
		}
		if text.Type != "text" {
			return fmt.Errorf("legacy skill metadata must be JSON text")
		}
		raw = []byte(text.Text)
	}
	if err != nil {
		return err
	}
	if len(raw) > 16<<20 {
		return fmt.Errorf("legacy skill metadata exceeds limit")
	}
	return json.Unmarshal(raw, out)
}
func (b *toolSkillBridge) ListSkills(ctx context.Context, cursor *string, opts ...client.RequestOption) (*schema.ListSkillsResult, error) {
	args := map[string]interface{}{}
	if cursor != nil {
		args["cursor"] = *cursor
	}
	out := &schema.ListSkillsResult{}
	if err := b.call(ctx, b.tools.List, args, out, opts...); err != nil {
		return nil, err
	}
	out.ResultType = "complete"
	ttl := 0
	out.TtlMs = &ttl
	out.CacheScope = "private"
	return out, nil
}
func (b *toolSkillBridge) GetSkill(ctx context.Context, uri string, opts ...client.RequestOption) (*schema.GetSkillResult, error) {
	out := &schema.GetSkillResult{}
	if err := b.call(ctx, b.tools.Get, map[string]interface{}{"uri": uri}, out, opts...); err != nil {
		return nil, err
	}
	out.ResultType = "complete"
	ttl := 0
	out.TtlMs = &ttl
	out.CacheScope = "private"
	return out, nil
}

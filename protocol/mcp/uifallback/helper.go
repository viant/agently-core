package uifallback

import (
	"context"
	"strings"

	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpuicompat "github.com/viant/mcp-ui/compat"
	mcpuimeta "github.com/viant/mcp-ui/meta"
)

type ResourceReader func(ctx context.Context, uri string) (*mcpschema.ReadResourceResult, error)

func ResourceURIFromStructuredContent(structured map[string]interface{}) string {
	if structured == nil {
		return ""
	}
	raw, _ := structured["resourceUri"].(string)
	uri := strings.TrimSpace(raw)
	if strings.HasPrefix(uri, "ui://") {
		return uri
	}
	return ""
}

func EmbeddedCallToolContent(
	ctx context.Context,
	clientCaps *mcpschema.ClientCapabilities,
	toolUI mcpuimeta.ToolUI,
	structured map[string]interface{},
	read ResourceReader,
) (*mcpschema.EmbeddedResource, bool, error) {
	if read == nil {
		return nil, false, nil
	}
	uri := ResourceURIFromStructuredContent(structured)
	if uri == "" {
		return nil, false, nil
	}
	// Stable Apps negotiation lives in extensions. The legacy adapter remains
	// selected only by its explicit fallback metadata below.
	if clientCaps != nil {
		if extension, ok := clientCaps.Extensions["io.modelcontextprotocol/ui"]; ok {
			switch values := extension["mimeTypes"].(type) {
			case []string:
				for _, value := range values {
					if value == "text/html;profile=mcp-app" {
						return nil, false, nil
					}
				}
			case []interface{}:
				for _, value := range values {
					if value == "text/html;profile=mcp-app" {
						return nil, false, nil
					}
				}
			}
		}
	}
	mode := mcpuicompat.SelectToolMode(clientCaps, toolUI)
	if mode == mcpuicompat.ModeCapability || mode == mcpuicompat.ModeNone {
		return nil, false, nil
	}
	embedded, err := mcpuicompat.BuildEmbeddedFallback(ctx, resourceReaderFunc(read), uri)
	if err != nil {
		return nil, false, err
	}
	return embedded, true, nil
}

type resourceReaderFunc ResourceReader

func (r resourceReaderFunc) ReadResource(ctx context.Context, uri string) (*mcpschema.ReadResourceResult, error) {
	return r(ctx, uri)
}

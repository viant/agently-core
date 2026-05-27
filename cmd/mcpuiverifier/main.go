// Command mcpuiverifier is a dedicated remote MCP server used to verify the
// MCP UI extension end-to-end against the canonical extension SDK.
//
// It is intentionally separate from the in-process agently-core demo tool so
// the final verification path exercises the real remote MCP transport
// (tools/list, resources/list, resources/read) rather than an in-process
// short-circuit.
//
// The server:
//
//   - advertises one tool, `show_widget`, whose `_meta.ui.resourceUri` points
//     at a `ui://...` HTML resource that this server itself publishes;
//   - exposes that resource through standard MCP `resources/list` and
//     `resources/read` so any host can fetch it;
//   - advertises UI extension capability under
//     `Experimental["io.modelcontextprotocol/ui"]` so capability negotiation
//     works end-to-end.
//
// Transports:
//
//   - default: stdio (workspace-friendly; spawn-on-demand)
//   - opt-in: streamable HTTP via -transport http -port <port>
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	mcpclient "github.com/viant/mcp-protocol/client"
	mcplogger "github.com/viant/mcp-protocol/logger"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpproto "github.com/viant/mcp-protocol/server"
	mcpserver "github.com/viant/mcp/server"

	mcpuiapp "github.com/viant/mcp-ui/appproto"
	mcpuicap "github.com/viant/mcp-ui/capabilities"
	mcpuimeta "github.com/viant/mcp-ui/meta"
	mcpuires "github.com/viant/mcp-ui/resource"
)

// Identity constants. The server-scope avoids the reserved `agently.*` prefix
// because this binary is a distinct MCP server identity, not Agently itself.
const (
	serverName    = "mcp-ui-verifier"
	serverVersion = "1.0.0"

	uiServerScope = "verifier.wk_0000000000000001"
	uiResourceURI = "ui://" + uiServerScope + "/widget/show_widget"

	toolName        = "show_widget"
	toolDescription = "Returns a verification payload and advertises the matching ui:// resource."

	// Allowed guest-originated tool calls when this widget is rendered.
	// Intentionally empty in this verification slice — the widget proves the
	// resource/render path; interactive guest tool calls are exercised by the
	// existing preview slice in agently-core.
)

var allowedGuestTools = []string{}

const widgetHTML = `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8" />
  <title>MCP UI Verifier</title>
</head>
<body>
  <main>
    <h1 id="title">MCP UI Verifier</h1>
    <p id="body">Static HTML resource published by the dedicated remote MCP UI verifier server. If you can read this through Agently's host-owned resource read endpoint, the remote resources/list and resources/read path is working end-to-end.</p>
    <p id="origin" style="margin-top:12px;color:#475467;font-size:14px;">origin: mcp-ui-verifier</p>
  </main>
</body>
</html>`

type showWidgetIn struct {
	Note string `json:"note,omitempty"`
}

type showWidgetOut struct {
	Status      string `json:"status"`
	ResourceURI string `json:"resourceUri"`
	Note        string `json:"note,omitempty"`
}

type handler struct {
	*mcpproto.DefaultHandler
}

func main() {
	transportKind := flag.String("transport", "stdio", "transport: stdio|http")
	port := flag.Int("port", 18095, "HTTP port when -transport=http")
	flag.Parse()

	ctx := context.Background()
	srv, err := mcpserver.New(
		mcpserver.WithImplementation(mcpschema.Implementation{
			Name:    serverName,
			Version: serverVersion,
		}),
		mcpserver.WithNewHandler(func(_ context.Context, n transport.Notifier, l mcplogger.Logger, ops mcpclient.Operations) (mcpproto.Handler, error) {
			h := &handler{DefaultHandler: mcpproto.NewDefaultHandler(n, l, ops)}
			h.advertiseUICapability()
			if err := h.register(); err != nil {
				return nil, err
			}
			return h, nil
		}),
	)
	if err != nil {
		log.Fatalf("create mcp server: %v", err)
	}

	switch *transportKind {
	case "stdio":
		stdioSrv := srv.Stdio(ctx)
		go forwardSignals(ctx)
		if err := stdioSrv.ListenAndServe(); err != nil {
			log.Fatalf("stdio listen: %v", err)
		}
	case "http":
		srv.UseStreamableHTTP(true)
		addr := fmt.Sprintf("127.0.0.1:%d", *port)
		httpSrv := srv.HTTP(ctx, addr)
		go func() {
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			<-sigCh
			_ = httpSrv.Shutdown(context.Background())
		}()
		log.Printf("mcp-ui-verifier http listening on %s", addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http listen: %v", err)
		}
	default:
		log.Fatalf("unsupported transport: %s", *transportKind)
	}
}

func forwardSignals(ctx context.Context) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sigCh:
		os.Exit(0)
	case <-ctx.Done():
	}
}

func (h *handler) register() error {
	if err := h.registerResource(); err != nil {
		return err
	}
	return h.registerTool()
}

func (h *handler) registerResource() error {
	desc := "MCP UI verifier widget — proves remote resources/list and resources/read end-to-end."
	title := "MCP UI Verifier"
	uiMeta := mcpuimeta.ResourceUI{
		AllowedTools:    allowedGuestTools,
		ContentHash:     mcpuires.ContentHash(widgetHTML),
		ProtocolVersion: mcpuiapp.Version,
	}
	resourceMeta, err := mcpuires.NewHTMLResource(uiResourceURI, toolName, &desc, &title, nil, uiMeta)
	if err != nil {
		return fmt.Errorf("build ui resource metadata: %w", err)
	}
	contents, err := mcpuires.NewReadResultHTMLContents(uiResourceURI, widgetHTML, uiMeta)
	if err != nil {
		return fmt.Errorf("build ui resource contents: %w", err)
	}
	h.RegisterResource(*resourceMeta,
		func(_ context.Context, req *mcpschema.ReadResourceRequest) (*mcpschema.ReadResourceResult, *jsonrpc.Error) {
			if req == nil || req.Params.Uri != uiResourceURI {
				return nil, jsonrpc.NewError(jsonrpc.InvalidParams, fmt.Sprintf("unknown resource uri: %v", req), nil)
			}
			return &mcpschema.ReadResourceResult{
				Contents: []mcpschema.ReadResourceResultContentsElem{*contents},
			}, nil
		},
	)
	return nil
}

func (h *handler) registerTool() error {
	if err := mcpproto.RegisterTool[*showWidgetIn, *showWidgetOut](
		h.Registry,
		toolName,
		toolDescription,
		func(_ context.Context, in *showWidgetIn) (*mcpschema.CallToolResult, *jsonrpc.Error) {
			note := ""
			if in != nil {
				note = in.Note
			}
			out := &showWidgetOut{
				Status:      "ok",
				ResourceURI: uiResourceURI,
				Note:        note,
			}
			return success(out)
		},
	); err != nil {
		return fmt.Errorf("register show_widget tool: %w", err)
	}
	entry, ok := h.ToolRegistry.Get(toolName)
	if !ok || entry == nil {
		return fmt.Errorf("show_widget tool entry missing after registration")
	}
	mcpuimeta.SetToolUI(&entry.Metadata, mcpuimeta.ToolUI{
		ResourceUri:  uiResourceURI,
		AllowedTools: allowedGuestTools,
	})
	return nil
}

func (h *handler) advertiseUICapability() {
	if h.ServerCapabilities == nil {
		h.ServerCapabilities = &mcpschema.ServerCapabilities{}
	}
	mcpuicap.SetServerCapability(h.ServerCapabilities, mcpuicap.Capability{
		ProtocolVersion: mcpuiapp.Version,
		MimeTypes:       []string{mcpuicap.ResourceMimeType},
	})
}

func success(out interface{}) (*mcpschema.CallToolResult, *jsonrpc.Error) {
	data, err := json.Marshal(out)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	structured := map[string]interface{}{}
	if err := json.Unmarshal(data, &structured); err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	return &mcpschema.CallToolResult{
		StructuredContent: structured,
		Content:           []mcpschema.CallToolResultContentElem{mcpschema.TextContent{Type: "text", Text: string(data)}},
	}, nil
}

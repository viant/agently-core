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
	"time"

	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	mcpclient "github.com/viant/mcp-protocol/client"
	mcplogger "github.com/viant/mcp-protocol/logger"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpproto "github.com/viant/mcp-protocol/server"
	mcpserver "github.com/viant/mcp/server"
)

type handler struct {
	*mcpproto.DefaultHandler
	mode string
	ops  mcpclient.Operations
}

type emptyIn struct{}

type approvalIn struct {
	Task string `json:"task,omitempty"`
}
type approvalOut struct {
	Mode   string `json:"mode"`
	Status string `json:"status"`
	Task   string `json:"task,omitempty"`
}
type resourceOut struct {
	Mode     string `json:"mode"`
	Status   string `json:"status"`
	Resource string `json:"resource"`
}

type elicitationOut struct {
	Mode    string                 `json:"mode"`
	Action  string                 `json:"action"`
	Content map[string]interface{} `json:"content,omitempty"`
}

const (
	formElicitationID = "mcp-form-e2e-elicitation"
	oobElicitationID  = "mcp-oob-e2e-elicitation"
)

func main() {
	var (
		port = flag.Int("port", 18091, "MCP HTTP port")
		mode = flag.String("mode", "resources", "server mode: resources|elicitation_form|elicitation_oob|approval_tool")
	)
	flag.Parse()

	ctx := context.Background()
	srv, err := mcpserver.New(
		mcpserver.WithRootRedirect(true),
		mcpserver.WithImplementation(mcpschema.Implementation{
			Name:    "agently-core-e2e-mcp",
			Version: "1.0.0",
		}),
		mcpserver.WithNewHandler(func(_ context.Context, n transport.Notifier, l mcplogger.Logger, ops mcpclient.Operations) (mcpproto.Handler, error) {
			base := mcpproto.NewDefaultHandler(n, l, ops)
			h := &handler{DefaultHandler: base, mode: *mode, ops: ops}
			if err := h.register(); err != nil {
				return nil, err
			}
			return h, nil
		}),
	)
	if err != nil {
		log.Fatalf("create mcp server: %v", err)
	}
	srv.UseStreamableHTTP(true)

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	httpSrv := srv.HTTP(ctx, addr)
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		_ = httpSrv.Shutdown(context.Background())
	}()

	log.Printf("e2e mcp server listening on %s mode=%s", addr, *mode)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("mcp listen: %v", err)
	}
}

func (h *handler) register() error {
	switch h.mode {
	case "resources":
		h.RegisterResource(mcpschema.Resource{Name: "about", Uri: "mcp:resources:/about"},
			func(_ context.Context, req *mcpschema.ReadResourceRequest) (*mcpschema.ReadResourceResult, *jsonrpc.Error) {
				return &mcpschema.ReadResourceResult{
					Contents: []mcpschema.ReadResourceResultContentsElem{
						{
							Uri:  req.Params.Uri,
							Text: "MCP resources server for agently-core e2e",
						},
					},
				}, nil
			})
		return mcpproto.RegisterTool[*emptyIn, *resourceOut](h.Registry, "resource_probe", "Returns deterministic MCP resource probe output",
			func(_ context.Context, _ *emptyIn) (*mcpschema.CallToolResult, *jsonrpc.Error) {
				out := &resourceOut{
					Mode:     "resources",
					Status:   "ok",
					Resource: "mcp:resources:/about",
				}
				return success(out)
			})
	case "elicitation_form":
		return mcpproto.RegisterTool[*emptyIn, *elicitationOut](h.Registry, "ask_form", "Requests form elicitation from client",
			func(ctx context.Context, _ *emptyIn) (*mcpschema.CallToolResult, *jsonrpc.Error) {
				request := formElicitRequest()
				result, err := h.ops.Elicit(ctx, request)
				if err != nil || result == nil {
					return errorResult(fmt.Sprintf("form elicitation failed: %v", err))
				}
				out := &elicitationOut{
					Mode:    "elicitation_form",
					Action:  string(result.Action),
					Content: result.Content,
				}
				return success(out)
			})
	case "elicitation_oob":
		return mcpproto.RegisterTool[*emptyIn, *elicitationOut](h.Registry, "ask_oob", "Requests URL/OOB elicitation from client",
			func(ctx context.Context, _ *emptyIn) (*mcpschema.CallToolResult, *jsonrpc.Error) {
				request := oobElicitRequest()
				result, err := h.ops.Elicit(ctx, request)
				if err != nil || result == nil {
					return errorResult(fmt.Sprintf("oob elicitation failed: %v", err))
				}
				out := &elicitationOut{
					Mode:    "elicitation_oob",
					Action:  string(result.Action),
					Content: result.Content,
				}
				return success(out)
			})
	case "approval_tool":
		return mcpproto.RegisterTool[*approvalIn, *approvalOut](h.Registry, "execute", "Executes a test approval task and returns deterministic output",
			func(_ context.Context, in *approvalIn) (*mcpschema.CallToolResult, *jsonrpc.Error) {
				task := "default"
				if in != nil && in.Task != "" {
					task = in.Task
				}
				out := &approvalOut{
					Mode:   "approval_tool",
					Status: "executed",
					Task:   task,
				}
				return success(out)
			})
	default:
		return fmt.Errorf("unsupported mode: %s", h.mode)
	}
}

func formElicitRequest() *jsonrpc.TypedRequest[*mcpschema.ElicitRequest] {
	return &jsonrpc.TypedRequest[*mcpschema.ElicitRequest]{
		Request: &mcpschema.ElicitRequest{
			Id:      mcpschema.RequestId(time.Now().UnixNano()),
			Jsonrpc: "2.0",
			Method:  mcpschema.MethodElicitationCreate,
			Params: mcpschema.ElicitRequestParams{
				ElicitationId: formElicitationID,
				Message:       "Please provide favoriteColor for MCP form tool validation.",
				Mode:          mcpschema.ElicitRequestParamsModeForm,
				RequestedSchema: mcpschema.ElicitRequestParamsRequestedSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"favoriteColor": map[string]interface{}{
							"type": "string",
						},
					},
					Required: []string{"favoriteColor"},
				},
			},
		},
	}
}

func oobElicitRequest() *jsonrpc.TypedRequest[*mcpschema.ElicitRequest] {
	return &jsonrpc.TypedRequest[*mcpschema.ElicitRequest]{
		Request: &mcpschema.ElicitRequest{
			Id:      mcpschema.RequestId(time.Now().UnixNano()),
			Jsonrpc: "2.0",
			Method:  mcpschema.MethodElicitationCreate,
			Params: mcpschema.ElicitRequestParams{
				ElicitationId: oobElicitationID,
				Message:       "Please confirm OOB flow for MCP tool validation.",
				Mode:          mcpschema.ElicitRequestParamsModeUrl,
				Url:           "https://example.com/mcp/oob-approval",
			},
		},
	}
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

func errorResult(message string) (*mcpschema.CallToolResult, *jsonrpc.Error) {
	errFlag := true
	return &mcpschema.CallToolResult{
		IsError: &errFlag,
		Content: []mcpschema.CallToolResultContentElem{mcpschema.TextContent{Type: "text", Text: message}},
	}, nil
}

package sdk

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/viant/agently-core/protocol/primitive/observe"
	identity "github.com/viant/agently-core/protocol/resource"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/sdk/api"
	dssvc "github.com/viant/agently-core/service/datasource"
)

func handleOperateDatasource(client Backend) http.HandlerFunc {
	return handleOperateDatasourceWithBindings(client, nil)
}
func handleOperateDatasourceWithBindings(client Backend, bindings *PublishedWindowBindings) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		started := time.Now()
		var timings *datasourceTimingCollector
		trace := os.Getenv("BACKEND_PRIMITIVE_TRACE") == "1"
		perf := r.URL.Query().Get("perf") == "1"
		if perf || trace {
			timings = &datasourceTimingCollector{trace: trace}
			r = r.WithContext(observe.WithObserver(r.Context(), timings))
		}
		if trace {
			defer func() { log.Printf("primitive datasource timing (nested spans overlap): %s", timings.traceJSON()) }()
		}
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("datasource id is required"))
			return
		}
		var in api.OperateDatasourceInput
		if err := decodeJSONBody(r, &in); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(in.Operation) == "" || strings.TrimSpace(in.Operation) != in.Operation {
			writeDatasourceFetchError(w, identity.ErrResourceDenied, nil)
			return
		}
		in.ID = id
		ctx := runtimerequestctx.WithUIBridgeSessionID(r.Context(), r.Header.Get("Mcp-Session-Id"))
		ctx = runtimerequestctx.WithUIWindowID(ctx, r.URL.Query().Get("windowId"))
		convID := strings.TrimSpace(in.ConversationID)
		if convID == "" {
			convID = strings.TrimSpace(r.URL.Query().Get("conversationId"))
		}
		if convID != "" {
			if err := authorizeDatasourceConversation(ctx, client, convID); err != nil {
				httpError(w, statusForDatasourceConversationErr(err), err)
				return
			}
			ctx = runtimerequestctx.WithConversationID(ctx, convID)
			in.ConversationID = convID
		}
		var out *api.OperateDatasourceOutput
		var err error
		var handled bool
		if bindings != nil && r.URL.Query().Get("windowId") != "" && r.Header.Get("Mcp-Session-Id") != "" {
			out, handled, err = bindings.Operate(ctx, r.URL.Query().Get("windowId"), in.ID, in.Operation, in.Inputs, in.WindowParameters)
		}
		if !handled {
			if r.URL.Query().Get("cohort") == "1" {
				if perf {
					setDatasourceTimingHeader(w, timings, started)
				}
				if bindings != nil && r.URL.Query().Get("windowId") != "" && r.Header.Get("Mcp-Session-Id") != "" {
					// A verified caller lost only its server-side window binding,
					// for example across a host restart. The UI may reopen the
					// metadata once; this endpoint never creates a binding.
					httpJSON(w, http.StatusForbidden, map[string]string{
						"error": "window binding required", "code": "window_binding_required", "scope": "window",
					})
					return
				}
				writeDatasourceFetchError(w, identity.ErrResourceDenied, nil)
				return
			}
			if bindings != nil && r.URL.Query().Get("windowId") != "" && r.Header.Get("Mcp-Session-Id") != "" {
				ctx = dssvc.WithUnboundWindowProviderGuard(ctx, bindings.protectedProviderIdentities())
			}
			operator, ok := client.(interface {
				OperateDatasource(context.Context, *api.OperateDatasourceInput) (*api.OperateDatasourceOutput, error)
			})
			if !ok {
				err = ErrDatasourceStackNotConfigured
			} else {
				out, err = operator.OperateDatasource(ctx, &in)
			}
		}
		if perf {
			setDatasourceTimingHeader(w, timings, started)
		}
		if err != nil {
			resource := in.Resource
			if handled {
				resource = nil
			} // browser provenance never classifies cohort denial
			writeDatasourceFetchError(w, err, resource)
			return
		}
		if out == nil {
			out = &api.OperateDatasourceOutput{}
		}
		httpJSON(w, http.StatusOK, out)
	}
}

package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	convdata "github.com/viant/agently-core/app/store/data"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/agently-core/protocol/primitive/observe"
	identity "github.com/viant/agently-core/protocol/resource"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/sdk/api"
	dssvc "github.com/viant/agently-core/service/datasource"
	"github.com/viant/agently-core/service/policy"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/authz"
	"github.com/viant/authz/gating"
	"github.com/viant/jsonrpc"
)

// ErrDatasourceStackNotConfigured is returned by Backend implementations that
// have not wired the datasource / overlay services. HTTP handlers translate
// it into 501 Not Implemented so clients can distinguish from transient
// errors.
var ErrDatasourceStackNotConfigured = errors.New("datasource stack not configured")

func handleFetchDatasource(client Backend) http.HandlerFunc {
	return handleFetchDatasourceWithBindings(client, nil)
}
func handleFetchDatasourceWithBindings(client Backend, bindings *PublishedWindowBindings) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		var in api.FetchDatasourceInput
		if err := decodeJSONBody(r, &in); err != nil {
			httpError(w, http.StatusBadRequest, err)
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
		var out *api.FetchDatasourceOutput
		var err error
		var handled bool
		if bindings != nil && r.URL.Query().Get("windowId") != "" && r.Header.Get("Mcp-Session-Id") != "" {
			out, handled, err = bindings.Fetch(ctx, r.URL.Query().Get("windowId"), in.ID, in.Inputs, in.WindowParameters)
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
			out, err = client.FetchDatasource(ctx, &in)
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
			out = &api.FetchDatasourceOutput{}
		}
		if out.Rows == nil {
			out.Rows = make([]map[string]interface{}, 0)
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleInvalidateDatasourceCache(client Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("datasource id is required"))
			return
		}
		inputsHash := strings.TrimSpace(r.URL.Query().Get("inputsHash"))
		if err := client.InvalidateDatasourceCache(r.Context(), &api.InvalidateDatasourceCacheInput{
			ID: id, InputsHash: inputsHash,
		}); err != nil {
			writeDatasourceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleListLookupRegistry(client Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctxParam := strings.TrimSpace(r.URL.Query().Get("context"))
		if ctxParam == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("context query parameter is required (e.g. ?context=template:foo)"))
			return
		}
		input := &api.ListLookupRegistryInput{Context: ctxParam, ProviderIdentity: r.URL.Query().Get("providerIdentity")}
		if owner := strings.TrimSpace(r.URL.Query().Get("owner")); owner != "" {
			input.Owner = &identity.ResourceRef{URI: owner, Revision: r.URL.Query().Get("revision")}
		}
		out, err := client.ListLookupRegistry(r.Context(), input)
		if err != nil {
			log.Printf("lookup registry diagnostic: %v", err)
			writeDatasourceError(w, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

// statusForDatasourceErr maps the sentinel "not configured" error to 501 so
// clients can distinguish it from genuine internal errors.
func statusForDatasourceErr(err error) int {
	var upstream *dsproto.UpstreamAuthorizationError
	var rpcError *jsonrpc.Error
	if errors.As(err, &rpcError) && isAuthStatus(rpcError.Code) {
		return rpcError.Code
	}
	if errors.Is(err, dsproto.ErrDatasourceDenied) {
		return http.StatusForbidden
	}
	if errors.As(err, &upstream) && (upstream.Status == 401 || upstream.Status == 403) {
		return upstream.Status
	}
	if errors.Is(err, policy.ErrDenied) || errors.Is(err, identity.ErrResourceDenied) || errors.Is(err, identity.ErrResourceStale) || errors.Is(err, authz.ErrDenied) {
		return http.StatusForbidden
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	if errors.Is(err, permittedview.ErrUnavailable) || errors.Is(err, authz.ErrUnavailable) || errors.Is(err, gating.ErrUnavailable) {
		return http.StatusServiceUnavailable
	}
	if errors.Is(err, ErrDatasourceStackNotConfigured) {
		return http.StatusNotImplemented
	}
	if status, ok := upstreamAuthStatusFromError(err); ok {
		return status
	}
	if status, ok := upstreamInputStatusFromError(err); ok {
		return status
	}
	return http.StatusInternalServerError
}

// Availability is classified by trusted sentinels; upstream payloads are omitted.
func writeDatasourceError(w http.ResponseWriter, err error) {
	status := statusForDatasourceErr(err)
	if isAuthStatus(status) {
		if errors.Is(err, dsproto.ErrDatasourceDenied) {
			err = dsproto.ErrDatasourceDenied
		} else {
			err = &dsproto.UpstreamAuthorizationError{Status: status, Cause: err}
		}
	}
	switch status {
	case http.StatusGatewayTimeout:
		err = context.DeadlineExceeded
	case http.StatusServiceUnavailable:
		err = permittedview.ErrUnavailable
	}
	httpError(w, status, err)
}

// A window artifact failure is distinct from managing-MCP data denial. The
// request selects the artifact kind; only trusted error types set this scope.
func writeDatasourceFetchError(w http.ResponseWriter, err error, resource *identity.ResolvedResource) {
	if resource != nil && statusForDatasourceErr(err) == http.StatusForbidden &&
		(errors.Is(err, identity.ErrResourceDenied) || errors.Is(err, identity.ErrResourceStale)) {
		uri, parseErr := identity.ParseResourceURI(resource.URI)
		if parseErr == nil && uri.Kind == "window" {
			httpJSON(w, http.StatusForbidden, map[string]string{
				"error": "window artifact access denied", "code": "resource_denied", "scope": "window",
			})
			return
		}
	}
	writeDatasourceError(w, err)
}

type datasourceErrorEnvelope struct {
	Status  string                  `json:"status"`
	Message string                  `json:"message"`
	Errors  []datasourceErrorDetail `json:"errors"`
}

type datasourceErrorDetail struct {
	View       string                  `json:"view"`
	Parameter  string                  `json:"parameter"`
	StatusCode int                     `json:"statusCode"`
	Message    string                  `json:"message"`
	Object     []datasourceErrorDetail `json:"object"`
}

// Tool and datasource HTTP endpoints preserve the same explicit validation
// provenance. Message text alone cannot supply a validation status.
func upstreamInputStatusFromError(err error) (int, bool) {
	var statusError interface{ HTTPStatusCode() int }
	if errors.As(err, &statusError) {
		status := statusError.HTTPStatusCode()
		if status == http.StatusBadRequest || status == http.StatusUnprocessableEntity {
			return status, true
		}
	}
	return 0, false
}

func upstreamAuthStatusFromError(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	var upstream *dsproto.UpstreamAuthorizationError
	if errors.As(err, &upstream) && isAuthStatus(upstream.Status) {
		return upstream.Status, true
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return 0, false
	}
	body := datasourceErrorBody(msg)
	if body == "" {
		return upstreamAuthStatusFromMessage(msg)
	}
	var envelope datasourceErrorEnvelope
	if json.Unmarshal([]byte(body), &envelope) != nil {
		return upstreamAuthStatusFromMessage(msg)
	}
	if status := datasourceDetailStatus(envelope.Errors); status != 0 {
		return status, true
	}
	return upstreamAuthStatusFromMessage(strings.TrimSpace(envelope.Message + " " + msg))
}

func datasourceErrorBody(msg string) string {
	if idx := strings.Index(msg, "{"); idx >= 0 {
		candidate := strings.TrimSpace(msg[idx:])
		if strings.HasPrefix(candidate, "{") {
			return candidate
		}
	}
	return ""
}

func datasourceDetailStatus(details []datasourceErrorDetail) int {
	best := 0
	for _, detail := range details {
		if isAuthStatus(detail.StatusCode) && detail.StatusCode > best {
			best = detail.StatusCode
		}
		if nested := datasourceDetailStatus(detail.Object); nested > best {
			best = nested
		}
	}
	return best
}

func isAuthStatus(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden
}

func upstreamAuthStatusFromMessage(msg string) (int, bool) {
	text := strings.ToLower(strings.TrimSpace(msg))
	if text == "" {
		return 0, false
	}
	switch {
	case strings.Contains(text, "authorization required"),
		strings.Contains(text, "unauthorized"),
		strings.Contains(text, "oauth session is missing a valid token"),
		strings.Contains(text, "missing usable oauth tokens"),
		strings.Contains(text, "missing valid token"):
		return http.StatusUnauthorized, true
	case strings.Contains(text, "forbidden"),
		strings.Contains(text, "permission denied"),
		strings.Contains(text, "user access denied"):
		return http.StatusForbidden, true
	default:
		return 0, false
	}
}

func authorizeDatasourceConversation(ctx context.Context, client Backend, conversationID string) error {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil
	}
	conversation, err := readConversationHeader(ctx, client, conversationID)
	if err != nil {
		return err
	}
	if conversation == nil {
		return convdata.ErrConversationNotFound
	}
	return nil
}

func statusForDatasourceConversationErr(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, convdata.ErrPermissionDenied):
		return http.StatusForbidden
	case errors.Is(err, convdata.ErrConversationNotFound):
		return http.StatusNotFound
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(msg, "permission denied"), strings.Contains(msg, "forbidden"):
		return http.StatusForbidden
	case strings.Contains(msg, "conversation not found"), strings.Contains(msg, "not found"):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

// decodeJSONBody is a small helper tolerant of empty bodies.
func decodeJSONBody(r *http.Request, out interface{}) error {
	if r.Body == nil {
		return nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

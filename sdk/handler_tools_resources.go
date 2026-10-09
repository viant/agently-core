package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/viant/agently-core/workspace"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	exportrequestmodel "github.com/viant/agently-core/model/exportrequest"
	identity "github.com/viant/agently-core/protocol/resource"
	toolpolicy "github.com/viant/agently-core/protocol/tool"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	svcauth "github.com/viant/agently-core/service/auth"
	resourcepolicy "github.com/viant/agently-core/service/policy"
	"github.com/viant/authz"
)

func handleListToolDefinitions(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if convID := strings.TrimSpace(r.URL.Query().Get("conversationId")); convID != "" {
			ctx = runtimerequestctx.WithConversationID(ctx, convID)
		}
		defs, err := client.ListToolDefinitions(ctx)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, defs)
	}
}

func handleListSkills(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := client.ListSkills(r.Context(), &ListSkillsInput{
			AgentID:        strings.TrimSpace(r.URL.Query().Get("agentId")),
			ConversationID: strings.TrimSpace(r.URL.Query().Get("conversationId")),
		})
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleSkillDiagnostics(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := client.GetSkillDiagnostics(r.Context())
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleActivateSkill(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSpace(r.PathValue("name"))
		if name == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("skill name is required"))
			return
		}
		var req struct {
			Args string `json:"args"`
		}
		if r.Body != nil && r.Body != http.NoBody {
			decoder := json.NewDecoder(r.Body)
			if err := decoder.Decode(&req); err != nil && err != io.EOF {
				httpError(w, http.StatusBadRequest, err)
				return
			}
			if err := decoder.Decode(&struct{}{}); err != io.EOF {
				httpError(w, http.StatusBadRequest, err)
				return
			}
		}
		out, err := client.ActivateSkill(r.Context(), &ActivateSkillInput{
			ConversationID: strings.TrimSpace(r.URL.Query().Get("conversationId")),
			Name:           name,
			Args:           req.Args,
		})
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleExecuteTool(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		name := strings.TrimSpace(r.PathValue("name"))
		if name == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("tool name is required"))
			return
		}
		var args map[string]interface{}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&args)
		}
		ctx := r.Context()
		ctx = withTrustedExportRequest(ctx, r)
		if convID := strings.TrimSpace(r.URL.Query().Get("conversationId")); convID != "" {
			ctx = runtimerequestctx.WithConversationID(ctx, convID)
		}
		ctx = ensureDirectToolPolicy(ctx)
		debugMCPExecf("http execute start name=%s argsKeys=%d", name, len(args))
		result, err := client.ExecuteTool(ctx, name, args)
		debugMCPExecf("http execute done name=%s elapsed=%s resultBytes=%d err=%v", name, time.Since(start).Round(time.Millisecond), len(result), err)
		if err != nil {
			writeToolExecuteError(w, err, result)
			return
		}
		httpJSON(w, http.StatusOK, map[string]string{"result": result})
		debugMCPExecf("http execute wrote response name=%s elapsed=%s", name, time.Since(start).Round(time.Millisecond))
	}
}

func handleExecuteToolByName(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		var req struct {
			Name string                 `json:"name"`
			Args map[string]interface{} `json:"args"`
		}
		if err := decodeJSON(r, &req); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("tool name is required"))
			return
		}
		ctx := r.Context()
		ctx = withTrustedExportRequest(ctx, r)
		if convID := strings.TrimSpace(r.URL.Query().Get("conversationId")); convID != "" {
			ctx = runtimerequestctx.WithConversationID(ctx, convID)
		}
		ctx = ensureDirectToolPolicy(ctx)
		debugMCPExecf("http execute-by-name start name=%s argsKeys=%d", name, len(req.Args))
		result, err := client.ExecuteTool(ctx, name, req.Args)
		debugMCPExecf("http execute-by-name done name=%s elapsed=%s resultBytes=%d err=%v", name, time.Since(start).Round(time.Millisecond), len(result), err)
		if err != nil {
			writeToolExecuteError(w, err, result)
			return
		}
		httpJSON(w, http.StatusOK, map[string]string{"result": result})
		debugMCPExecf("http execute-by-name wrote response name=%s elapsed=%s", name, time.Since(start).Round(time.Millisecond))
	}
}

func withTrustedExportRequest(ctx context.Context, r *http.Request) context.Context {
	requestID := ""
	if r != nil {
		requestID = strings.TrimSpace(r.Header.Get(exportrequestmodel.Header))
	}
	if requestID == "" {
		requestID = exportrequestmodel.NewID()
	}
	return exportrequestmodel.WithID(ctx, requestID)
}

func debugMCPExecf(format string, args ...interface{}) {
	if strings.TrimSpace(os.Getenv("AGENTLY_DEBUG_MCP_EXEC")) == "" {
		return
	}
	_, _ = fmt.Fprintf(os.Stderr, "[mcp-exec] "+format+"\n", args...)
}

func resourcePathRef(r *http.Request) (*ResourceRef, error) {
	kind := strings.TrimSpace(r.PathValue("kind"))
	name := strings.TrimSpace(r.PathValue("name"))
	if kind == "" || name == "" {
		return nil, fmt.Errorf("resource kind and name are required")
	}
	return &ResourceRef{Kind: kind, Name: name}, nil
}

func handleListPendingToolApprovals(client Client, authConfigs ...*svcauth.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var authorized bool
		r, authorized = applicationRequestPrincipal(w, r, q.Get("userId"), authConfigs)
		if !authorized {
			return
		}
		userID, ok := scopedToolApprovalUserID(r.Context(), strings.TrimSpace(q.Get("userId")))
		if !ok {
			httpError(w, http.StatusForbidden, fmt.Errorf("permission denied"))
			return
		}
		rows, err := client.ListPendingToolApprovals(r.Context(), &ListPendingToolApprovalsInput{
			UserID:         userID,
			ConversationID: strings.TrimSpace(q.Get("conversationId")),
			Status:         strings.TrimSpace(q.Get("status")),
			Limit:          queryInt(q.Get("limit")),
			Offset:         queryInt(q.Get("offset")),
			OutcomeSince:   strings.TrimSpace(q.Get("outcomeSince")),
		})
		if err != nil {
			if isToolApprovalQueueNotConfiguredErr(err) {
				httpJSON(w, http.StatusOK, &PendingToolApprovalPage{Rows: []*PendingToolApproval{}, Total: 0, Offset: 0, Limit: 0, HasMore: false})
				return
			}
			httpError(w, statusForToolApprovalErr(err), err)
			return
		}
		httpJSON(w, http.StatusOK, rows)
	}
}

func queryInt(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func isToolApprovalQueueNotConfiguredErr(err error) bool {
	msg := strings.ToLower(strings.TrimSpace(fmt.Sprint(err)))
	return strings.Contains(msg, "tool approval queue not configured")
}

func handleDecideToolApproval(client Client, authConfigs ...*svcauth.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("approval id is required"))
			return
		}
		var body DecideToolApprovalInput
		if err := decodeJSON(r, &body); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		var authorized bool
		r, authorized = applicationRequestPrincipal(w, r, body.UserID, authConfigs)
		if !authorized {
			return
		}
		userID, ok := scopedToolApprovalUserID(r.Context(), strings.TrimSpace(body.UserID))
		if !ok {
			httpError(w, http.StatusForbidden, fmt.Errorf("permission denied"))
			return
		}
		body.ID = id
		body.UserID = userID
		out, err := client.DecideToolApproval(r.Context(), &body)
		if err != nil {
			httpError(w, statusForToolApprovalErr(err), err)
			return
		}
		if out != nil && out.Protocol == nil && out.Outcome != nil && strings.EqualFold(strings.TrimSpace(out.Outcome.Status), "failed") {
			message := strings.TrimSpace(out.Outcome.ErrorMessage)
			if message == "" {
				message = "tool approval execution failed"
			}
			httpError(w, http.StatusConflict, errors.New(message))
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func applicationRequestPrincipal(w http.ResponseWriter, r *http.Request, explicit string, configs []*svcauth.Config) (*http.Request, bool) {
	if iauth.EffectiveUserID(r.Context()) != "" {
		return r, true
	}
	var config *svcauth.Config
	if len(configs) > 0 {
		config = configs[0]
	}
	if config != nil && config.Enabled {
		httpError(w, http.StatusUnauthorized, fmt.Errorf("authorization required"))
		return r, false
	}
	// Local mode retains explicit-user compatibility. Browser requests without
	// one share the same anonymous cookie principal as conversation/AG-UI routes.
	principal := resolveQueryUserID(w, r, explicit, config)
	return r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: principal})), true
}

func scopedToolApprovalUserID(ctx context.Context, explicit string) (string, bool) {
	derived := strings.TrimSpace(iauth.EffectiveUserID(ctx))
	if derived == "" {
		return strings.TrimSpace(explicit), true
	}
	if strings.TrimSpace(explicit) != "" && !strings.EqualFold(strings.TrimSpace(explicit), derived) {
		return "", false
	}
	return derived, true
}

func statusForToolApprovalErr(err error) int {
	msg := strings.ToLower(strings.TrimSpace(fmt.Sprint(err)))
	if strings.Contains(msg, "permission denied") || strings.Contains(msg, "forbidden") {
		return http.StatusForbidden
	}
	if strings.Contains(msg, "already exists") || strings.Contains(msg, "conflict") {
		return http.StatusConflict
	}
	if strings.Contains(msg, "not found") {
		return http.StatusNotFound
	}
	if strings.Contains(msg, "required") || strings.Contains(msg, "invalid") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func statusForToolExecuteError(err error) int {
	// Identity errors can also wrap a policy denial; preserve that distinction.
	if errors.Is(err, authz.ErrIdentityDenied) || errors.Is(err, resourcepolicy.ErrIdentityRejected) {
		return http.StatusUnauthorized
	}
	if errors.Is(err, authz.ErrDenied) || errors.Is(err, resourcepolicy.ErrDenied) || errors.Is(err, identity.ErrResourceDenied) {
		return http.StatusForbidden
	}
	if errors.Is(err, authz.ErrUnavailable) {
		return http.StatusServiceUnavailable
	}
	if toolpolicy.IsPolicyError(err) {
		return http.StatusConflict
	}
	if status, ok := upstreamAuthStatusFromError(err); ok {
		return status
	}
	msg := strings.ToLower(strings.TrimSpace(fmt.Sprint(err)))
	if strings.Contains(msg, "permission denied") || strings.Contains(msg, "forbidden") {
		return http.StatusForbidden
	}
	if strings.Contains(msg, "already exists") || strings.Contains(msg, "conflict") {
		return http.StatusConflict
	}
	if strings.Contains(msg, "not found") {
		return http.StatusNotFound
	}
	if strings.Contains(msg, "required") || strings.Contains(msg, "invalid") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func writeToolExecuteError(w http.ResponseWriter, err error, result string) {
	status := statusForToolExecuteError(err)
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		// Partial definitions/data cannot accompany failed authorization.
		result = ""
	}
	httpErrorWithResult(w, status, err, result)
}

func ensureDirectToolPolicy(ctx context.Context) context.Context {
	if toolpolicy.FromContext(ctx) != nil {
		return ctx
	}
	return toolpolicy.WithPolicy(ctx, &toolpolicy.Policy{Mode: toolpolicy.ModeBestPath})
}

func handleListResources(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		out, err := client.ListResources(r.Context(), &ListResourcesInput{Kind: kind})
		if err != nil {
			httpError(w, workspaceResourceErrorStatus(err), err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleGetResource(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ref, err := resourcePathRef(r)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		out, err := client.GetResource(r.Context(), ref)
		if err != nil {
			httpError(w, workspaceResourceErrorStatus(err), err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleSaveResource(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ref, err := resourcePathRef(r)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if err := client.SaveResource(r.Context(), &SaveResourceInput{Kind: ref.Kind, Name: ref.Name, Data: body}); err != nil {
			httpError(w, workspaceResourceErrorStatus(err), err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleDeleteResource(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ref, err := resourcePathRef(r)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if err := client.DeleteResource(r.Context(), ref); err != nil {
			httpError(w, workspaceResourceErrorStatus(err), err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleExportResources(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input ExportResourcesInput
		if err := decodeJSON(r, &input); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		out, err := client.ExportResources(r.Context(), &input)
		if err != nil {
			httpError(w, workspaceResourceErrorStatus(err), err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleImportResources(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input ImportResourcesInput
		if err := decodeJSON(r, &input); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		out, err := client.ImportResources(r.Context(), &input)
		if err != nil {
			httpError(w, workspaceResourceErrorStatus(err), err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func workspaceResourceErrorStatus(err error) int {
	if errors.Is(err, workspace.ErrCanonicalResourceAccess) {
		return http.StatusForbidden
	}
	return http.StatusInternalServerError
}

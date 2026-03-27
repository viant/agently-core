package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	toolpolicy "github.com/viant/agently-core/protocol/tool"
	"github.com/viant/agently-core/runtime/memory"
)

func handleListToolDefinitions(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defs, err := client.ListToolDefinitions(r.Context())
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, defs)
	}
}

func handleExecuteTool(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		var args map[string]interface{}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&args)
		}
		ctx := r.Context()
		if convID := strings.TrimSpace(r.URL.Query().Get("conversationId")); convID != "" {
			ctx = memory.WithConversationID(ctx, convID)
		}
		ctx = ensureDirectToolPolicy(ctx)
		result, err := client.ExecuteTool(ctx, name, args)
		if err != nil {
			httpError(w, statusForToolExecuteError(err), err)
			return
		}
		httpJSON(w, http.StatusOK, map[string]string{"result": result})
	}
}

func handleExecuteToolByName(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		if convID := strings.TrimSpace(r.URL.Query().Get("conversationId")); convID != "" {
			ctx = memory.WithConversationID(ctx, convID)
		}
		ctx = ensureDirectToolPolicy(ctx)
		result, err := client.ExecuteTool(ctx, name, req.Args)
		if err != nil {
			httpError(w, statusForToolExecuteError(err), err)
			return
		}
		httpJSON(w, http.StatusOK, map[string]string{"result": result})
	}
}

func handleListPendingToolApprovals(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		rows, err := client.ListPendingToolApprovals(r.Context(), &ListPendingToolApprovalsInput{
			UserID:         strings.TrimSpace(q.Get("userId")),
			ConversationID: strings.TrimSpace(q.Get("conversationId")),
			Status:         strings.TrimSpace(q.Get("status")),
		})
		if err != nil {
			if isToolApprovalQueueNotConfiguredErr(err) {
				httpJSON(w, http.StatusOK, map[string]interface{}{"data": []interface{}{}})
				return
			}
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, map[string]interface{}{"data": rows})
	}
}

func isToolApprovalQueueNotConfiguredErr(err error) bool {
	msg := strings.ToLower(strings.TrimSpace(fmt.Sprint(err)))
	return strings.Contains(msg, "tool approval queue not configured")
}

func handleDecideToolApproval(client Client) http.HandlerFunc {
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
		body.ID = id
		out, err := client.DecideToolApproval(r.Context(), &body)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func statusForToolExecuteError(err error) int {
	if toolpolicy.IsPolicyError(err) {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
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
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleGetResource(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.PathValue("kind")
		name := r.PathValue("name")
		out, err := client.GetResource(r.Context(), &ResourceRef{Kind: kind, Name: name})
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleSaveResource(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.PathValue("kind")
		name := r.PathValue("name")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if err := client.SaveResource(r.Context(), &SaveResourceInput{Kind: kind, Name: name, Data: body}); err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleDeleteResource(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.PathValue("kind")
		name := r.PathValue("name")
		if err := client.DeleteResource(r.Context(), &ResourceRef{Kind: kind, Name: name}); err != nil {
			httpError(w, http.StatusInternalServerError, err)
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
			httpError(w, http.StatusInternalServerError, err)
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
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

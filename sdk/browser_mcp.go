package sdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	iauth "github.com/viant/agently-core/internal/auth"
	svcauth "github.com/viant/agently-core/service/auth"
	"github.com/viant/agently-core/service/browsermcp"
)

func registerBrowserMCPRoutes(mux *http.ServeMux, client Client, authCfg *svcauth.Config, registry *browsermcp.Registry) {
	mux.HandleFunc("POST /v1/mcp/browser/catalog", func(w http.ResponseWriter, r *http.Request) {
		user := resolveQueryUserID(w, r, "", authCfg)
		if user == "" {
			httpError(w, 401, errors.New("authorization required"))
			return
		}
		ctx := r.Context()
		if iauth.EffectiveUserID(ctx) == "" {
			ctx = iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: user})
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256*1024))
		if err != nil {
			httpError(w, 400, browsermcp.ErrUnavailable)
			return
		}
		var input browsermcp.Registration
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&input) != nil || d.Decode(new(any)) != io.EOF {
			httpError(w, 400, browsermcp.ErrUnavailable)
			return
		}
		conv, err := client.GetConversation(ctx, input.ConversationID)
		if err != nil || conv == nil || conv.Id != input.ConversationID || conv.CreatedByUserId == nil || *conv.CreatedByUserId != user {
			httpError(w, 403, browsermcp.ErrUnavailable)
			return
		}
		runtime, ok := client.(aguiRuntime)
		if !ok {
			httpError(w, 503, browsermcp.ErrUnavailable)
			return
		}
		thread, err := runtime.aguiStore().GetThread(ctx, user, input.ThreadID)
		if err != nil || thread == nil || thread.ConversationID != input.ConversationID {
			httpError(w, 403, browsermcp.ErrUnavailable)
			return
		}
		result, err := registry.Register(ctx, user, input)
		if err != nil {
			httpError(w, 403, browsermcp.ErrUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("GET /v1/mcp/browser/catalog/{id}", func(w http.ResponseWriter, r *http.Request) {
		user := resolveQueryUserID(w, r, "", authCfg)
		if user == "" {
			httpError(w, 401, errors.New("authorization required"))
			return
		}
		ctx := r.Context()
		if iauth.EffectiveUserID(ctx) == "" {
			ctx = iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: user})
		}
		catalog, err := registry.Current(ctx, user, r.PathValue("id"))
		if err != nil {
			httpError(w, 403, browsermcp.ErrUnavailable)
			return
		}
		conv, err := client.GetConversation(ctx, catalog.ConversationID)
		if err != nil || conv == nil || conv.CreatedByUserId == nil || *conv.CreatedByUserId != user {
			httpError(w, 403, browsermcp.ErrUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(catalog)
	})
	mux.HandleFunc("DELETE /v1/mcp/browser/catalog/{id}", func(w http.ResponseWriter, r *http.Request) {
		user := resolveQueryUserID(w, r, "", authCfg)
		if user == "" {
			httpError(w, 401, errors.New("authorization required"))
			return
		}
		if registry.Revoke(user, r.PathValue("id")) != nil {
			httpError(w, 404, browsermcp.ErrUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

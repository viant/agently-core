package sdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

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
		conv, err := readConversationHeader(ctx, client, input.ConversationID)
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
		view := r.URL.Query().Get("view")
		if view != "" && view != "status" {
			httpError(w, 400, errors.New("unsupported browser catalog projection"))
			return
		}
		started := time.Now()
		var catalog browsermcp.Catalog
		var status browsermcp.CatalogStatus
		var err error
		if view == "status" {
			status, err = registry.CurrentStatus(ctx, user, r.PathValue("id"))
			catalog.ConversationID = status.ConversationID
		} else {
			catalog, err = registry.Current(ctx, user, r.PathValue("id"))
		}
		w.Header().Set("Server-Timing", fmt.Sprintf("catalog;dur=%.3f", float64(time.Since(started).Microseconds())/1000))
		w.Header().Set("Cache-Control", "no-store")
		if err != nil {
			httpError(w, 403, err)
			return
		}
		ownershipStarted := time.Now()
		conv, err := readConversationHeader(ctx, client, catalog.ConversationID)
		w.Header().Set("Server-Timing", w.Header().Get("Server-Timing")+fmt.Sprintf(", ownership;dur=%.3f", float64(time.Since(ownershipStarted).Microseconds())/1000))
		if err != nil || conv == nil {
			httpError(w, 403, errors.New("browser MCP catalog unavailable for this conversation (conversationUnavailable)"))
			return
		}
		if conv.CreatedByUserId == nil || *conv.CreatedByUserId != user {
			httpError(w, 403, errors.New("browser MCP catalog unavailable for this conversation (conversationOwnerMismatch)"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if view == "status" {
			json.NewEncoder(w).Encode(status)
		} else {
			json.NewEncoder(w).Encode(catalog)
		}
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

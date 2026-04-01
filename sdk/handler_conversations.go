package sdk

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func handleCreateConversation(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input CreateConversationInput
		if err := decodeJSON(r, &input); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		out, err := client.CreateConversation(r.Context(), &input)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleGetConversation(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		out, err := client.GetConversation(r.Context(), id)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleUpdateConversation(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		var body struct {
			Title      string `json:"title"`
			Visibility string `json:"visibility"`
			Shareable  *bool  `json:"shareable"`
		}
		if err := decodeJSON(r, &body); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		input := &UpdateConversationInput{
			ConversationID: id,
			Title:          strings.TrimSpace(body.Title),
			Visibility:     strings.TrimSpace(body.Visibility),
			Shareable:      body.Shareable,
		}
		out, err := client.UpdateConversation(r.Context(), input)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleGetTranscript(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		q := r.URL.Query()
		input := &GetTranscriptInput{
			ConversationID:    id,
			Since:             q.Get("since"),
			IncludeModelCalls: q.Get("includeModelCalls") == "true" || q.Get("includeModelCall") == "true",
			IncludeToolCalls:  q.Get("includeToolCalls") == "true" || q.Get("includeToolCall") == "true",
		}
		var opts []TranscriptOption
		if rawSelectors := strings.TrimSpace(q.Get("selectors")); rawSelectors != "" {
			var decoded map[string]*QuerySelector
			if err := json.Unmarshal([]byte(rawSelectors), &decoded); err != nil {
				httpError(w, http.StatusBadRequest, fmt.Errorf("invalid selectors"))
				return
			}
			for name, selector := range decoded {
				opts = append(opts, WithTranscriptSelector(name, selector))
			}
		}
		if q.Get("includeFeeds") == "true" {
			opts = append(opts, WithIncludeFeeds())
		}
		out, err := client.GetTranscript(r.Context(), input, opts...)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleListConversations(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		input := &ListConversationsInput{
			AgentID:          strings.TrimSpace(q.Get("agentId")),
			ParentID:         strings.TrimSpace(q.Get("parentId")),
			ParentTurnID:     strings.TrimSpace(q.Get("parentTurnId")),
			ExcludeScheduled: queryBool(r, "excludeScheduled", false),
			Query:            strings.TrimSpace(q.Get("q")),
			Status:           strings.TrimSpace(q.Get("status")),
		}
		if limitRaw := strings.TrimSpace(q.Get("limit")); limitRaw != "" {
			limit, err := strconv.Atoi(limitRaw)
			if err != nil || limit <= 0 {
				httpError(w, http.StatusBadRequest, fmt.Errorf("invalid limit"))
				return
			}
			if input.Page == nil {
				input.Page = &PageInput{}
			}
			input.Page.Limit = limit
		}
		if cursor := strings.TrimSpace(q.Get("cursor")); cursor != "" {
			if input.Page == nil {
				input.Page = &PageInput{}
			}
			input.Page.Cursor = cursor
		}
		if direction := strings.TrimSpace(q.Get("direction")); direction != "" {
			if input.Page == nil {
				input.Page = &PageInput{}
			}
			input.Page.Direction = Direction(direction)
		}
		out, err := client.ListConversations(r.Context(), input)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleListLinkedConversations(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		parentID := strings.TrimSpace(q.Get("parentConversationId"))
		parentTurnID := strings.TrimSpace(q.Get("parentTurnId"))
		if parentID == "" && parentTurnID == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("parentConversationId or parentTurnId is required"))
			return
		}
		input := &ListLinkedConversationsInput{
			ParentConversationID: parentID,
			ParentTurnID:         parentTurnID,
		}
		if limitRaw := strings.TrimSpace(q.Get("limit")); limitRaw != "" {
			limit, err := strconv.Atoi(limitRaw)
			if err != nil || limit <= 0 {
				httpError(w, http.StatusBadRequest, fmt.Errorf("invalid limit"))
				return
			}
			if input.Page == nil {
				input.Page = &PageInput{}
			}
			input.Page.Limit = limit
		}
		if cursor := strings.TrimSpace(q.Get("cursor")); cursor != "" {
			if input.Page == nil {
				input.Page = &PageInput{}
			}
			input.Page.Cursor = cursor
		}
		if direction := strings.TrimSpace(q.Get("direction")); direction != "" {
			if input.Page == nil {
				input.Page = &PageInput{}
			}
			input.Page.Direction = Direction(direction)
		}
		out, err := client.ListLinkedConversations(r.Context(), input)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleGetLiveState(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		q := r.URL.Query()
		var opts []TranscriptOption
		if q.Get("includeFeeds") == "true" {
			opts = append(opts, WithIncludeFeeds())
		}
		out, err := client.GetLiveState(r.Context(), id, opts...)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleTerminate(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		if err := client.TerminateConversation(r.Context(), id); err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleCompact(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		if err := client.CompactConversation(r.Context(), id); err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handlePrune(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		if err := client.PruneConversation(r.Context(), id); err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

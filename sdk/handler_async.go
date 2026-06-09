package sdk

import (
	"fmt"
	"net/http"
	"strings"
)

func statusForAsyncError(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if isNotFoundError(err) {
		return http.StatusNotFound
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(msg, "required") || strings.Contains(msg, "invalid") || strings.Contains(msg, "unsupported") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func handleListAsyncOperations(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		out, err := client.ListAsyncOperations(r.Context(), &ListAsyncOperationsInput{
			ConversationID: id,
			Tool:           strings.TrimSpace(r.URL.Query().Get("tool")),
			Mode:           strings.TrimSpace(r.URL.Query().Get("mode")),
		})
		if err != nil {
			httpError(w, statusForAsyncError(err), err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

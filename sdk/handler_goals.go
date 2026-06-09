package sdk

import (
	"fmt"
	"net/http"
	"strings"
)

func statusForGoalError(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if isConflictError(err) {
		return http.StatusConflict
	}
	if isFeatureDisabledError(err) {
		return http.StatusForbidden
	}
	if isNotFoundError(err) {
		return http.StatusNotFound
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(msg, "required") || strings.Contains(msg, "invalid") || strings.Contains(msg, "unsupported") {
		return http.StatusBadRequest
	}
	if strings.Contains(msg, "does not exist") {
		return http.StatusNotFound
	}
	if strings.Contains(msg, "already exists") {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func handleGetGoal(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		out, err := client.GetGoal(r.Context(), id)
		if err != nil {
			httpError(w, statusForGoalError(err), err)
			return
		}
		httpJSON(w, http.StatusOK, &GoalEnvelope{Goal: out})
	}
}

func handleCreateGoal(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		var input CreateGoalInput
		if err := decodeJSON(r, &input); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		input.ConversationID = id
		out, err := client.CreateGoal(r.Context(), &input)
		if err != nil {
			httpError(w, statusForGoalError(err), err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleUpdateGoal(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		var input UpdateGoalInput
		if err := decodeJSON(r, &input); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		input.ConversationID = id
		out, err := client.UpdateGoal(r.Context(), &input)
		if err != nil {
			httpError(w, statusForGoalError(err), err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleClearGoal(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		if err := client.ClearGoal(r.Context(), id); err != nil {
			httpError(w, statusForGoalError(err), err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

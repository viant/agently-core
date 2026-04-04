package sdk

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
)

func conversationAndTurnIDs(r *http.Request) (string, string, error) {
	conversationID := strings.TrimSpace(r.PathValue("id"))
	turnID := strings.TrimSpace(r.PathValue("turnId"))
	if conversationID == "" || turnID == "" {
		return "", "", fmt.Errorf("conversation ID and turn ID are required")
	}
	return conversationID, turnID, nil
}

func handleCancelTurn(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		cancelled, err := client.CancelTurn(r.Context(), id)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, map[string]bool{"cancelled": cancelled})
	}
}

func handleSteerTurn(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID, turnID, err := conversationAndTurnIDs(r)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		var input SteerTurnInput
		if err = decodeJSON(r, &input); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		input.ConversationID = conversationID
		input.TurnID = turnID
		out, err := client.SteerTurn(context.WithoutCancel(r.Context()), &input)
		if err != nil {
			if isConflictError(err) {
				httpError(w, http.StatusConflict, err)
				return
			}
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		if out == nil {
			out = &SteerTurnOutput{TurnID: turnID, Status: "accepted"}
		}
		httpJSON(w, http.StatusAccepted, out)
	}
}

func handleDeleteQueuedTurn(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID, turnID, err := conversationAndTurnIDs(r)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if err = client.CancelQueuedTurn(r.Context(), conversationID, turnID); err != nil {
			if isConflictError(err) {
				httpError(w, http.StatusConflict, err)
				return
			}
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func handleMoveQueuedTurn(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID, turnID, err := conversationAndTurnIDs(r)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		var input MoveQueuedTurnInput
		if err = decodeJSON(r, &input); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		input.ConversationID = conversationID
		input.TurnID = turnID
		if err = client.MoveQueuedTurn(r.Context(), &input); err != nil {
			if isConflictError(err) {
				httpError(w, http.StatusConflict, err)
				return
			}
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func handleEditQueuedTurn(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID, turnID, err := conversationAndTurnIDs(r)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		var input EditQueuedTurnInput
		if err = decodeJSON(r, &input); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		input.ConversationID = conversationID
		input.TurnID = turnID
		if err = client.EditQueuedTurn(r.Context(), &input); err != nil {
			if isConflictError(err) {
				httpError(w, http.StatusConflict, err)
				return
			}
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func handleForceSteerQueuedTurn(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID, turnID, err := conversationAndTurnIDs(r)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		out, err := client.ForceSteerQueuedTurn(r.Context(), conversationID, turnID)
		if err != nil {
			if isConflictError(err) {
				httpError(w, http.StatusConflict, err)
				return
			}
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		if out == nil {
			out = &SteerTurnOutput{TurnID: turnID, Status: "accepted"}
		}
		httpJSON(w, http.StatusAccepted, out)
	}
}

func handleResolveElicitation(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID := strings.TrimSpace(r.PathValue("conversationId"))
		elicitationID := strings.TrimSpace(r.PathValue("elicitationId"))
		if conversationID == "" || elicitationID == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversationId and elicitationId are required"))
			return
		}
		var body struct {
			Action  string                 `json:"action"`
			Payload map[string]interface{} `json:"payload"`
		}
		if err := decodeJSON(r, &body); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		in := &ResolveElicitationInput{
			ConversationID: conversationID,
			ElicitationID:  elicitationID,
			Action:         strings.TrimSpace(body.Action),
			Payload:        body.Payload,
		}
		if in.Action == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("action is required"))
			return
		}
		if err := client.ResolveElicitation(r.Context(), in); err != nil {
			log.Printf("[resolve-elicitation] convID=%s elicitID=%s error: %v", conversationID, elicitationID, err)
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

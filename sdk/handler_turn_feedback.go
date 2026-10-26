package sdk

import (
	"errors"
	"net/http"

	"github.com/viant/agently-core/app/store/data"
	svcauth "github.com/viant/agently-core/service/auth"
)

func handleTurnFeedback(client Client, authConfigs ...*svcauth.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		r, ok = applicationRequestPrincipal(w, r, "", authConfigs)
		if !ok {
			return
		}
		conversationID, turnID, err := conversationAndTurnIDs(r)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		var out *TurnFeedback
		if r.Method == http.MethodDelete {
			out, err = client.ClearTurnFeedback(r.Context(), conversationID, turnID)
		} else {
			var input SetTurnFeedbackInput
			if err = decodeJSON(r, &input); err != nil {
				httpError(w, http.StatusBadRequest, err)
				return
			}
			if err = validateTurnFeedback(&input); err != nil {
				httpError(w, http.StatusBadRequest, err)
				return
			}
			input.ConversationID, input.TurnID = conversationID, turnID
			out, err = client.SetTurnFeedback(r.Context(), &input)
		}
		if err != nil {
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, errInvalidTurnFeedback):
				status = http.StatusBadRequest
			case errors.Is(err, errTurnFeedbackUnauthenticated):
				status = http.StatusUnauthorized
			case errors.Is(err, data.ErrPermissionDenied):
				status = http.StatusForbidden
			case errors.Is(err, errTurnFeedbackNotFound):
				status = http.StatusNotFound
			case errors.Is(err, errTurnFeedbackUnavailable):
				status = http.StatusConflict
			}
			httpError(w, status, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

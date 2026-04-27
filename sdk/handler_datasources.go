package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/viant/agently-core/sdk/api"
)

// ErrDatasourceStackNotConfigured is returned by Backend implementations that
// have not wired the datasource / overlay services. HTTP handlers translate
// it into 501 Not Implemented so clients can distinguish from transient
// errors.
var ErrDatasourceStackNotConfigured = errors.New("datasource stack not configured")

func handleFetchDatasource(client Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		out, err := client.FetchDatasource(r.Context(), &in)
		if err != nil {
			httpError(w, statusForDatasourceErr(err), err)
			return
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
			httpError(w, statusForDatasourceErr(err), err)
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
		out, err := client.ListLookupRegistry(r.Context(), &api.ListLookupRegistryInput{Context: ctxParam})
		if err != nil {
			httpError(w, statusForDatasourceErr(err), err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

// statusForDatasourceErr maps the sentinel "not configured" error to 501 so
// clients can distinguish it from genuine internal errors.
func statusForDatasourceErr(err error) int {
	if errors.Is(err, ErrDatasourceStackNotConfigured) {
		return http.StatusNotImplemented
	}
	if status, ok := upstreamAuthStatusFromError(err); ok {
		return status
	}
	return http.StatusInternalServerError
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

func upstreamAuthStatusFromError(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return 0, false
	}
	body := datasourceErrorBody(msg)
	if body == "" {
		return 0, false
	}
	var envelope datasourceErrorEnvelope
	if json.Unmarshal([]byte(body), &envelope) != nil {
		return 0, false
	}
	if status := datasourceDetailStatus(envelope.Errors); status != 0 {
		return status, true
	}
	return 0, false
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

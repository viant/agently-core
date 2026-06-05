package sdk

import (
	"fmt"
	"net/http"
	"strings"
)

func handleListTemplates(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := client.ListTemplates(r.Context(), &ListTemplatesInput{})
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleGetTemplate(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSpace(r.PathValue("name"))
		if name == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("template name is required"))
			return
		}
		var includeDocument *bool
		if _, ok := r.URL.Query()["includeDocument"]; ok {
			value := queryBool(r, "includeDocument", false)
			includeDocument = &value
		}
		out, err := client.GetTemplate(r.Context(), &GetTemplateInput{
			Name:            name,
			IncludeDocument: includeDocument,
		})
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

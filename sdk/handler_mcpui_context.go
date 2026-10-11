package sdk

import (
	"context"
	"encoding/json"
	"errors"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/types"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// MCPUIResourceReadInput carries an untrusted reference to an opened window.
// Only the installed host binder may validate its target proof and authorize it.
// Ordinary apps retain the existing URI-only GET route.
type MCPUIResourceReadInput struct {
	URI            string                     `json:"uri"`
	Resource       *identity.ResolvedResource `json:"resource"`
	ResourceTarget *types.WindowTarget        `json:"resourceTarget"`
}
type MCPUIResourceContextBinder func(context.Context, *MCPUIResourceReadInput) (context.Context, error)

func WithMCPUIResourceContextBinder(bind MCPUIResourceContextBinder) HandlerOption {
	return func(c *handlerConfig) { c.mcpUIResourceContextBinder = bind }
}

func handleMCPUIResourceContextRead(reader MCPUIResourceReader, bind MCPUIResourceContextBinder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if bind == nil {
			httpError(w, http.StatusNotFound, errors.New("window resource context is not installed"))
			return
		}
		var in MCPUIResourceReadInput
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&in); err != nil {
			httpError(w, http.StatusBadRequest, errors.New("invalid window resource read"))
			return
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			httpError(w, http.StatusBadRequest, errors.New("invalid trailing window resource read"))
			return
		}
		in.URI = strings.TrimSpace(in.URI)
		if in.URI == "" || in.Resource == nil || in.ResourceTarget == nil {
			httpError(w, http.StatusBadRequest, errors.New("opened window resource and target are required"))
			return
		}
		ctx, err := bind(r.Context(), &in)
		if err != nil {
			httpError(w, http.StatusForbidden, err)
			return
		}
		if ctx == nil {
			httpError(w, http.StatusForbidden, errors.New("window resource context unavailable"))
			return
		}
		forwarded := r.Clone(ctx)
		forwarded.URL.RawQuery = "uri=" + url.QueryEscape(in.URI)
		handleMCPUIResourceRead(reader)(w, forwarded)
	}
}

package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	forgeservice "github.com/viant/agently-core/service/primitiveprovider"
	identity "github.com/viant/agently-core/protocol/resource"
	forgetypes "github.com/viant/forge/backend/types"
)

type WindowResourceProvider interface {
	UsesWindowResourceResolution() bool
	WindowDefinitionGet(context.Context, *forgeservice.WindowDefinitionGetInput) (*forgeservice.WindowDefinitionGetOutput, error)
	WindowDefinitionsList(context.Context, *forgeservice.WindowDefinitionListInput) (*forgeservice.WindowDefinitionListOutput, error)
}
type HandlerOption func(*handlerOptions)
type handlerOptions struct{ windows WindowResourceProvider }

func WithWindowResourceProvider(provider WindowResourceProvider) HandlerOption {
	return func(options *handlerOptions) { options.windows = provider }
}

func decodeResourceQuery(r *http.Request, name string, target any) error {
	values, found := r.URL.Query()[name]
	if !found {
		return nil
	}
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 8192 {
		return identity.ErrResource
	}
	decoder := json.NewDecoder(bytes.NewBufferString(values[0]))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		return identity.ErrResource
	}
	return nil
}
func resolvedHTTPWindow(r *http.Request, provider WindowResourceProvider, key string) (*forgetypes.Window, error) {
	var ref *identity.ResourceRef
	var pin *identity.ResolvedResource
	if err := decodeResourceQuery(r, "resourceRef", &ref); err != nil {
		return nil, err
	}
	if err := decodeResourceQuery(r, "resolvedResource", &pin); err != nil {
		return nil, err
	}
	requested := targetContextFromRequest(r)
	target := &forgetypes.WindowTarget{Platform: requested.Platform, FormFactor: requested.FormFactor, Surface: requested.Surface, Capabilities: requested.Capabilities}
	output, err := provider.WindowDefinitionGet(r.Context(), &forgeservice.WindowDefinitionGetInput{WindowID: key, Resource: ref, ResolvedResource: pin, Target: target})
	if err != nil {
		return nil, err
	}
	if output == nil || output.Definition == nil || output.Definition.Resource == nil {
		return nil, identity.ErrResourceDenied
	}
	return output.Definition, nil
}

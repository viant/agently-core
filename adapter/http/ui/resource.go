package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	identity "github.com/viant/agently-core/protocol/resource"
	forgeservice "github.com/viant/agently-core/service/primitiveprovider"
	forgetypes "github.com/viant/forge/backend/types"
)

type WindowResourceProvider interface {
	UsesWindowResourceResolution() bool
	WindowDefinitionGet(context.Context, *forgeservice.WindowDefinitionGetInput) (*forgeservice.WindowDefinitionGetOutput, error)
	WindowDefinitionsList(context.Context, *forgeservice.WindowDefinitionListInput) (*forgeservice.WindowDefinitionListOutput, error)
}
type HandlerOption func(*handlerOptions)
type handlerOptions struct {
	windows   WindowResourceProvider
	published map[string]*publishedWindowRegistration
}

// PublishedWindowSource opens a producer-validated closed window from the
// host's locally admitted cohort. The source is bound by host configuration;
// it is never selected by a browser-supplied provider name alone.
type PublishedWindowSource interface {
	OpenWindowVariant(context.Context, string, *forgetypes.WindowTarget) (*forgetypes.WindowResourceVariant, string, error)
}

// PublishedWindowBinder registers the selected inline datasource descriptors
// under the existing authenticated UI session for the SDK execution route.
type PublishedWindowBinder func(context.Context, string, string, string, string, string, *forgetypes.WindowResourceVariant) error

type publishedWindowRegistration struct {
	providerIdentity string
	source           PublishedWindowSource
	bind             PublishedWindowBinder
	namespaces       map[string]bool
}

func WithArtifactCohortWindowSource(providerIdentity string, source PublishedWindowSource, bind PublishedWindowBinder) HandlerOption {
	return WithArtifactCohortWindowSourceForNamespaces(providerIdentity, source, bind)
}

// WithArtifactCohortWindowSourceForNamespaces claims namespaces from trusted
// host configuration. A canonical legacy URL in one of these namespaces must
// use the admitted local source even when an old browser snapshot omitted its
// provider locator. Claims remain in force after a root is revoked or removed.
func WithArtifactCohortWindowSourceForNamespaces(providerIdentity string, source PublishedWindowSource, bind PublishedWindowBinder, namespaces ...string) HandlerOption {
	return func(options *handlerOptions) {
		if providerIdentity != "" && source != nil {
			if options.published == nil {
				options.published = map[string]*publishedWindowRegistration{}
			}
			claims := map[string]bool{}
			for _, namespace := range namespaces {
				if namespace != "" {
					claims[namespace] = true
				}
			}
			options.published[providerIdentity] = &publishedWindowRegistration{providerIdentity: providerIdentity, source: source, bind: bind, namespaces: claims}
		}
	}
}

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
	providerIdentity := r.URL.Query().Get("providerIdentity")
	namespace := r.URL.Query().Get("namespace")
	if r.URL.Query().Get("expectedResource") != "" {
		expected, err := decodeExpectedWindowResource(r)
		if err != nil || pin != nil {
			return nil, identity.ErrResourceDenied
		}
		selected := identity.ResourceRef{URI: expected.URI, Revision: expected.ResourceCandidate.Selector()}
		if ref != nil && *ref != selected || providerIdentity != "" && providerIdentity != expected.ProviderIdentity {
			return nil, identity.ErrResourceDenied
		}
		ref = &selected
		originalURI, parseErr := identity.ParseResourceURI(expected.URI)
		if parseErr != nil || originalURI.Kind != "window" || namespace != "" && namespace != originalURI.Namespace {
			return nil, identity.ErrResourceDenied
		}
		providerIdentity = expected.ProviderIdentity
		if providerIdentity != "" && namespace == "" {
			namespace = originalURI.Namespace
		}
	}
	requested := targetContextFromRequest(r)
	// Discovery already supplied an exact owner and resource. Preserve that
	// locator instead of making the catalog enumerate windows for a short URL key.
	if ref != nil && providerIdentity != "" {
		uri, err := identity.ParseResourceURI(ref.URI)
		if err != nil || uri.Kind != "window" || namespace != "" && namespace != uri.Namespace {
			return nil, identity.ErrResource
		}
		if key == uri.Name {
			key = ref.URI
		}
	}
	target := &forgetypes.WindowTarget{Platform: requested.Platform, FormFactor: requested.FormFactor, Surface: requested.Surface, Capabilities: requested.Capabilities}
	output, err := provider.WindowDefinitionGet(r.Context(), &forgeservice.WindowDefinitionGetInput{WindowID: key, Resource: ref, ResolvedResource: pin, Target: target, Namespace: namespace, ProviderIdentity: providerIdentity})
	if err != nil {
		return nil, err
	}
	if output == nil || output.Definition == nil || output.Definition.Resource == nil {
		return nil, identity.ErrResourceDenied
	}
	return output.Definition, nil
}

// expectedWindowResource is selector integrity for a fresh OPEN, never authority.
// In particular the expired lease and old authority binding are not accepted.
type expectedWindowResource struct {
	URI              string `json:"uri"`
	ProviderIdentity string `json:"providerIdentity,omitempty"`
	identity.ResourceCandidate
}

func decodeExpectedWindowResource(r *http.Request) (*expectedWindowResource, error) {
	var expected *expectedWindowResource
	if decodeResourceQuery(r, "expectedResource", &expected) != nil || expected == nil || !expected.ResourceCandidate.Valid() || expected.URI == "" {
		return nil, identity.ErrResourceDenied
	}
	return expected, nil
}
func checkExpectedWindowResource(r *http.Request, resource *identity.ResolvedResource) error {
	if r.URL.Query().Get("expectedResource") == "" {
		return nil
	}
	expected, err := decodeExpectedWindowResource(r)
	if err != nil || resource == nil || expected.URI != resource.URI || expected.ProviderIdentity != resource.ProviderIdentity || expected.ResourceCandidate != resource.ResourceCandidate {
		return identity.ErrResourceDenied
	}
	return nil
}

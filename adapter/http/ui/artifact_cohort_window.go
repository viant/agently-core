package ui

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	forgetypes "github.com/viant/forge/backend/types"
)

// Bump when host-side projection of a published window changes. The producer
// hash identifies immutable source bytes; this tag identifies HTTP bytes.
const publishedWindowRepresentationVersion = "cohort-v2"

// tryPublishedWindow handles only operator-registered providers and namespace
// claims. Unknown namespaces continue through the existing route. A denied or
// unavailable claimed root never falls back to legacy reads.
func tryPublishedWindow(w http.ResponseWriter, r *http.Request, registrations map[string]*publishedWindowRegistration, pathParts []string) bool {
	if len(registrations) == 0 {
		return false
	}
	windowKey := strings.Join(pathParts, "/")
	var ref *identity.ResourceRef
	refErr := decodeResourceQuery(r, "resourceRef", &ref)
	var expected *expectedWindowResource
	if r.URL.Query().Get("expectedResource") != "" {
		expected, _ = decodeExpectedWindowResource(r)
	}
	claimNamespace := ""
	if parsed, err := parsePublishedWindowURI(windowKey); err == nil {
		claimNamespace = parsed.namespace
	} else if len(pathParts) == 1 {
		claimNamespace = r.URL.Query().Get("namespace")
	}
	if claimNamespace == "" && refErr == nil && ref != nil {
		if parsed, err := parsePublishedWindowURI(ref.URI); err == nil {
			claimNamespace = parsed.namespace
		}
	}
	if claimNamespace == "" && expected != nil {
		if parsed, err := parsePublishedWindowURI(expected.URI); err == nil {
			claimNamespace = parsed.namespace
		}
	}
	claimed, claimCount := claimedPublishedWindow(registrations, claimNamespace)
	if claimCount > 1 {
		http.Error(w, "window not found or access denied", http.StatusForbidden)
		return true
	}
	statedProvider := r.URL.Query().Get("providerIdentity")
	selectedProvider, selectedURI := "", ""
	if r.URL.Query().Get("expectedResource") != "" {
		expected, err := decodeExpectedWindowResource(r)
		if err != nil {
			if registrations[statedProvider] == nil && claimCount == 0 {
				return false
			}
			http.Error(w, "window not found or access denied", http.StatusForbidden)
			return true
		}
		selectedProvider, selectedURI = expected.ProviderIdentity, expected.URI
		if selectedProvider == "" && claimCount == 1 {
			selectedProvider = claimed.providerIdentity
		}
	} else if registrations[statedProvider] != nil {
		if refErr != nil {
			http.Error(w, "window not found or access denied", http.StatusForbidden)
			return true
		}
		selectedProvider = statedProvider
		if ref != nil {
			selectedURI = ref.URI
		} else if claimCount == 1 && claimed.providerIdentity == statedProvider {
			selectedURI = claimedWindowURI(windowKey, claimNamespace)
		} else {
			http.Error(w, "window not found or access denied", http.StatusForbidden)
			return true
		}
	} else if claimCount == 1 {
		if statedProvider != "" || refErr != nil {
			http.Error(w, "window not found or access denied", http.StatusForbidden)
			return true
		}
		selectedProvider = claimed.providerIdentity
		selectedURI = claimedWindowURI(windowKey, claimNamespace)
		if ref != nil {
			selectedURI = ref.URI
		}
	} else {
		return false
	}
	registration := registrations[selectedProvider]
	if registration == nil {
		if claimCount != 0 {
			http.Error(w, "window not found or access denied", http.StatusForbidden)
			return true
		}
		return false
	}
	if claimCount == 1 && registration != claimed {
		http.Error(w, "window not found or access denied", http.StatusForbidden)
		return true
	}
	if statedProvider != "" && statedProvider != registration.providerIdentity {
		http.Error(w, "window not found or access denied", http.StatusForbidden)
		return true
	}
	uri, err := parsePublishedWindowURI(selectedURI)
	if err != nil || (windowKey != uri.name && windowKey != selectedURI) || r.URL.Query().Get("namespace") != "" && r.URL.Query().Get("namespace") != uri.namespace {
		http.Error(w, "window not found or access denied", http.StatusForbidden)
		return true
	}
	requested := targetContextFromRequest(r)
	target := &forgetypes.WindowTarget{Platform: requested.Platform, FormFactor: requested.FormFactor, Surface: requested.Surface, Capabilities: requested.Capabilities}
	variant, hash, err := registration.source.OpenWindowVariant(r.Context(), selectedURI, target)
	if err != nil || variant == nil || variant.Window == nil || hash == "" {
		http.Error(w, "window not found or access denied", http.StatusForbidden)
		return true
	}
	if err := markPublishedDatasourceServices(variant); err != nil {
		http.Error(w, "window not found or access denied", http.StatusForbidden)
		return true
	}
	if registration.bind != nil && (r.URL.Query().Get("clientId") != "" || r.URL.Query().Get("windowId") != "") {
		if r.Header.Get("Mcp-Session-Id") == "" || r.URL.Query().Get("clientId") == "" || r.URL.Query().Get("windowId") == "" {
			http.Error(w, "window not found or access denied", http.StatusForbidden)
			return true
		}
		bindCtx := runtimerequestctx.WithUIBridgeSessionID(r.Context(), r.Header.Get("Mcp-Session-Id"))
		if err := registration.bind(bindCtx, r.URL.Query().Get("clientId"), r.URL.Query().Get("windowId"), registration.providerIdentity, selectedURI, hash, variant); err != nil {
			http.Error(w, "window not found or access denied", http.StatusForbidden)
			return true
		}
	}
	representationETag := `"` + hash + "." + publishedWindowRepresentationVersion + `"`
	w.Header().Set("ETag", representationETag)
	w.Header().Set("X-Artifact-Hash", hash)
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Vary", "Authorization, Cookie")
	if r.Header.Get("If-None-Match") == representationETag {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	variant.Window.SelectionBinding = nil
	variant.Window.LocalStateOwner = nil
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(permittedWindowResponse{Status: "ok", Data: forgetypes.ConsumerWindow(variant.Window)})
	return true
}

func claimedPublishedWindow(registrations map[string]*publishedWindowRegistration, namespace string) (*publishedWindowRegistration, int) {
	if namespace == "" {
		return nil, 0
	}
	var match *publishedWindowRegistration
	count := 0
	for _, registration := range registrations {
		if registration.namespaces[namespace] {
			match = registration
			count++
		}
	}
	return match, count
}

func claimedWindowURI(windowKey, namespace string) string {
	if _, err := parsePublishedWindowURI(windowKey); err == nil {
		return windowKey
	}
	return "window://" + namespace + "/" + windowKey
}

// cohort=1 is a routing hint for the existing SDK fetch endpoint. It grants
// nothing; the SDK still requires the server-side binding and current local
// admission. Only services belonging to this selected published window change.
func markPublishedDatasourceServices(variant *forgetypes.WindowResourceVariant) error {
	if variant == nil || variant.Window == nil {
		return nil
	}
	window := variant.Window
	if window.DataSource == nil {
		window.DataSource = map[string]forgetypes.DataSource{}
	}
	for id, raw := range variant.DataSources {
		var descriptor dsproto.DataSource
		if id == "" || json.Unmarshal(raw, &descriptor) != nil || descriptor.ID != "" && descriptor.ID != id {
			return identity.ErrResource
		}
		entry, exists := window.DataSource[id]
		if !exists {
			entry = descriptor.DataSource
		}
		if descriptor.Backend != nil {
			if descriptor.Backend.Kind == dsproto.BackendInline {
				entry.Service = nil
				noFetch := false
				entry.AutoFetch = &noFetch
			} else if descriptor.Backend.Kind != "" {
				entry.Service = &forgetypes.Service{Endpoint: "agentlyAPI", URI: "/v1/api/datasources/" + url.PathEscape(id) + "/fetch?cohort=1", Method: "POST"}
			}
		}
		window.DataSource[id] = entry
	}
	for id, source := range window.DataSource {
		markPublishedService(source.Service)
		window.DataSource[id] = source
	}
	return nil
}
func markPublishedService(service *forgetypes.Service) {
	if service == nil {
		return
	}
	for _, current := range []*forgetypes.Service{service, service.Post} {
		if current == nil || current.Endpoint != "agentlyAPI" {
			continue
		}
		parsed, err := url.Parse(current.URI)
		if err != nil || !strings.HasPrefix(parsed.Path, "/v1/api/datasources/") || !strings.HasSuffix(parsed.Path, "/fetch") {
			continue
		}
		query := parsed.Query()
		query.Set("cohort", "1")
		parsed.RawQuery = query.Encode()
		current.URI = parsed.String()
	}
}

type publishedWindowURI struct{ namespace, name string }

func parsePublishedWindowURI(raw string) (publishedWindowURI, error) {
	uri, err := identity.ParseResourceURI(raw)
	if err != nil || uri.Kind != "window" || uri.Namespace == "" || uri.Name == "" {
		return publishedWindowURI{}, identity.ErrResource
	}
	return publishedWindowURI{namespace: uri.Namespace, name: uri.Name}, nil
}

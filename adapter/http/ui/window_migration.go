package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	identity "github.com/viant/agently-core/protocol/resource"
)

// WindowMigrationTarget identifies a resource owner, never an authorization grant.
type WindowMigrationTarget struct {
	ProviderIdentity string `json:"providerIdentity"`
	URI              string `json:"uri"`
}

type WindowMigration struct {
	From WindowMigrationTarget `json:"from"`
	To   WindowMigrationTarget `json:"to"`
}

type windowMigrationKey struct{ provider, uri string }

// WithWindowMigrations prepares explicit operator-authored relocations once.
// Every restored window still opens through its destination's normal cohort path.
func WithWindowMigrations(entries []WindowMigration) (HandlerOption, error) {
	index := make(map[windowMigrationKey]WindowMigrationTarget, len(entries))
	for _, entry := range entries {
		for _, endpoint := range []WindowMigrationTarget{entry.From, entry.To} {
			u, err := identity.ParseResourceURI(endpoint.URI)
			if err != nil || u.Kind != "window" || endpoint.ProviderIdentity == "" || strings.TrimSpace(endpoint.ProviderIdentity) != endpoint.ProviderIdentity {
				return nil, fmt.Errorf("invalid window migration endpoint")
			}
		}
		key := windowMigrationKey{entry.From.ProviderIdentity, entry.From.URI}
		if _, exists := index[key]; exists || entry.From == entry.To {
			return nil, fmt.Errorf("duplicate or self-referencing window migration")
		}
		index[key] = entry.To
	}
	for _, target := range index {
		if _, chained := index[windowMigrationKey{target.ProviderIdentity, target.URI}]; chained {
			return nil, fmt.Errorf("window migrations must target a final resource")
		}
	}
	return func(options *handlerOptions) { options.migrations = index }, nil
}

func migrateWindowRequest(r *http.Request, key string, index map[windowMigrationKey]WindowMigrationTarget) (*http.Request, error) {
	if len(index) == 0 {
		return r, nil
	}
	u, err := identity.ParseResourceURI(key)
	if err != nil || u.Kind != "window" {
		return r, nil
	}
	q := r.URL.Query()
	provider := q.Get("providerIdentity")
	var ref *identity.ResourceRef
	var pin *identity.ResolvedResource
	if err := decodeResourceQuery(r, "resourceRef", &ref); err != nil {
		return nil, err
	}
	if err := decodeResourceQuery(r, "resolvedResource", &pin); err != nil {
		return nil, err
	}
	if pin != nil {
		if pin.URI != key || provider != "" && provider != pin.ProviderIdentity {
			return nil, identity.ErrResource
		}
		provider = pin.ProviderIdentity
	}
	if ref != nil && ref.URI != key {
		return nil, identity.ErrResource
	}
	var expected *expectedWindowResource
	if q.Get("expectedResource") != "" {
		expected, err = decodeExpectedWindowResource(r)
		if err != nil {
			return nil, err
		}
		if expected.URI != key || provider != "" && provider != expected.ProviderIdentity {
			return nil, identity.ErrResource
		}
		provider = expected.ProviderIdentity
	}
	target, ok := index[windowMigrationKey{provider, key}]
	if !ok {
		return r, nil
	}
	if ns := q.Get("namespace"); ns != "" && ns != u.Namespace {
		return nil, identity.ErrResource
	}
	// Historical pins describe the removed source. They cannot authorize the
	// destination or be reused as its revision/proof. Select it afresh.
	q.Del("resolvedResource")
	q.Del("expectedResource")
	selected, _ := json.Marshal(identity.ResourceRef{URI: target.URI})
	q.Set("resourceRef", string(selected))
	q.Set("providerIdentity", target.ProviderIdentity)
	destination, _ := identity.ParseResourceURI(target.URI)
	q.Set("namespace", destination.Namespace)
	copy := r.Clone(r.Context())
	urlCopy := *r.URL
	urlCopy.Path = "/window/" + target.URI
	urlCopy.RawPath = ""
	urlCopy.RawQuery = q.Encode()
	copy.URL = &urlCopy
	return copy, nil
}

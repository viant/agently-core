package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	identity "github.com/viant/agently-core/protocol/resource"
	"gopkg.in/yaml.v3"
)

// LayoutMenuBinding explicitly publishes only a layout's existing applications.
// The trusted path and logical URI are host configuration, never MCP arguments.
// This adapter has only a working source; authored storage may use any source
// with menu bindings and supply working/stamped candidates through the resolver.
func LayoutMenuBinding(path, uri, providerIdentity string, policy LocalRevisionPolicyFactory) (LocalResourceBinding, error) {
	parsed, err := identity.ParseResourceURI(uri)
	if err != nil || parsed.Kind != "menu" || providerIdentity == "" || policy == nil || !filepath.IsAbs(path) {
		return LocalResourceBinding{}, identity.ErrResource
	}
	source := &layoutMenuSource{path: path, uri: uri}
	return LocalResourceBinding{URI: uri, Title: "Navigation", FormatVersion: 1, Resolver: func(ctx context.Context, actor identity.VerifiedActor, action string) (*identity.ResourceResolver, error) {
		revisionPolicy, err := policy(ctx, actor, parsed, action)
		if err != nil {
			return nil, err
		}
		if revisionPolicy == nil {
			return nil, identity.ErrResourceDenied
		}
		return &identity.ResourceResolver{ProviderIdentity: providerIdentity, Source: source, Policy: revisionPolicy}, nil
	}}, nil
}

type layoutMenuSource struct{ path, uri string }

func (s *layoutMenuSource) read(ctx context.Context, uri identity.ResourceURI) (json.RawMessage, error) {
	if ctx.Err() != nil || uri.String() != s.uri {
		return nil, identity.ErrResourceDenied
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	if len(raw) > 4<<20 {
		return nil, fmt.Errorf("layout menu exceeds size limit")
	}
	var layout struct {
		Applications []map[string]interface{} `yaml:"applications"`
	}
	if err = yaml.Unmarshal(raw, &layout); err != nil {
		return nil, err
	}
	if layout.Applications == nil {
		return nil, identity.ErrResource
	}
	value, err := json.Marshal(map[string]interface{}{"schemaVersion": 1, "applications": layout.Applications})
	if err != nil {
		return nil, err
	}
	if err = ValidateMenu(1, value); err != nil {
		return nil, err
	}
	return value, nil
}
func (s *layoutMenuSource) Candidates(ctx context.Context, uri identity.ResourceURI) ([]identity.ResourceCandidate, error) {
	raw, err := s.read(ctx, uri)
	if err != nil {
		return nil, err
	}
	return []identity.ResourceCandidate{{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint(raw)}}, nil
}
func (s *layoutMenuSource) ReadCandidate(ctx context.Context, uri identity.ResourceURI, candidate identity.ResourceCandidate) (json.RawMessage, error) {
	raw, err := s.read(ctx, uri)
	if err != nil {
		return nil, err
	}
	if candidate.Kind != identity.WorkingCandidate || candidate.Revision != "" || identity.ContentFingerprint(raw) != candidate.ContentFingerprint {
		return nil, identity.ErrResourceStale
	}
	return raw, nil
}

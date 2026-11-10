package resources

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	resource "github.com/viant/agently-core/service/resource"
	embopt "github.com/viant/embedius/matching/option"
)

const knowledgePageSize = 100

// The native catalog binds its cursor to its effective page size. Keep that
// limit when configured roots occupied part of the initial response.
type knowledgeRootsCursor struct {
	Native string `json:"native,omitempty"`
	Limit  int    `json:"limit"`
	Max    int    `json:"max"`
}

func (s *Service) appendPublishedKnowledgePage(ctx context.Context, cursor string, max int, roots []Root, configured map[string]Root, output *RootsOutput) error {
	*output = RootsOutput{}
	if s.augmenter == nil {
		output.Roots = roots
		return nil
	}
	state := knowledgeRootsCursor{Limit: knowledgePageSize, Max: max}
	room := knowledgePageSize
	if max > 0 {
		room = max - len(roots)
		state.Limit = min(knowledgePageSize, max)
	}
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(raw, &state) != nil || state.Max != max || state.Limit <= 0 || state.Limit > knowledgePageSize || (max > 0 && state.Limit > max) {
			return primitive.ErrCatalogCursor
		}
	} else if room > 0 {
		state.Limit = min(state.Limit, room)
	}
	limit := state.Limit
	if room == 0 {
		limit = 1
	}
	page, err := s.augmenter.PublishedKnowledgeRoots(ctx, resource.CatalogQuery{Limit: limit, Cursor: state.Native})
	if err != nil {
		return err
	}
	if room > 0 {
		for _, row := range page.Items {
			uri := row.Resource.URI
			if root, found := configured[uri]; found {
				roots = append(roots, root)
			} else {
				roots = append(roots, discoveredKnowledgeRoot(uri, row.Resource.Description))
			}
		}
		state.Native = page.NextCursor
	}
	output.Roots = roots
	if state.Native != "" || (room == 0 && len(page.Items) > 0) {
		raw, _ := json.Marshal(state)
		output.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return nil
}

func discoveredKnowledgeRoot(uri, description string) Root {
	if strings.TrimSpace(description) == "" {
		description = "Published knowledge"
	}
	return Root{URI: uri, ID: uri, Description: description, AllowedSemanticSearch: true, Role: "user"}
}

// Find the longest admitted parent in this namespace's prepared catalog.
// An overly broad result requires an explicit rootId rather than guessing.
func (s *Service) publishedKnowledgeParent(ctx context.Context, uri string) (string, error) {
	if s.augmenter == nil {
		return "", identity.ErrResourceDenied
	}
	rootText, relative, ok := strings.Cut(strings.TrimPrefix(uri, "knowledge://"), "/")
	if !ok {
		return "", identity.ErrResourceDenied
	}
	name, _, ok := strings.Cut(relative, "/")
	if !ok {
		return "", identity.ErrResourceDenied
	}
	head := "knowledge://" + rootText + "/" + name
	if _, err := identity.ParseResourceURI(head); err != nil || !s.augmenter.PublishedKnowledgeClaims(head) {
		return "", identity.ErrResourceDenied
	}
	page, err := s.augmenter.PublishedKnowledgeRoots(ctx, resource.CatalogQuery{Namespace: rootText, NameTerms: []string{name}, Limit: knowledgePageSize})
	if err != nil {
		return "", err
	}
	if !page.Complete {
		return "", fmt.Errorf("knowledge URI parent lookup requires rootId")
	}
	parent := ""
	for _, row := range page.Items {
		root := row.Resource.URI
		if strings.HasPrefix(uri, root+"/") && len(root) > len(parent) {
			parent = root
		}
	}
	if parent == "" {
		return "", identity.ErrResourceDenied
	}
	return parent, nil
}

// Explicit selections can use discovered roots. Implicit search retains the
// configured surface and never indexes a newly discovered corpus on its own.
func (s *Service) addExplicitKnowledgeRoots(roots []Root, input *MatchInput) ([]Root, error) {
	selected := input.RootIDs
	if len(selected) == 0 {
		selected = append(append([]string(nil), input.RootURI...), input.Roots...)
	}
	for _, raw := range selected {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var configured *Root
		for i := range roots {
			root := &roots[i]
			if isKnowledgeRoot(root.URI) && (root.URI == raw || (!isKnowledgeRoot(raw) && strings.EqualFold(root.ID, raw))) {
				configured = root
				break
			}
		}
		if configured != nil {
			if !configured.AllowedSemanticSearch {
				return nil, identity.ErrResourceDenied
			}
			continue
		}
		if !isKnowledgeRoot(raw) {
			continue
		}
		if _, err := identity.ParseResourceURI(raw); err != nil || s.augmenter == nil || !s.augmenter.PublishedKnowledgeClaims(raw) {
			return nil, identity.ErrResourceDenied
		}
		roots = append(roots, discoveredKnowledgeRoot(raw, ""))
	}
	return roots, nil
}

// Caller exclusions may narrow a configured corpus. They cannot replace its
// inclusions or lift its size limit. Native exclusions apply in declaration
// order, so the configured exclusions retain precedence.
func scopedKnowledgeMatch(configured, requested *embopt.Options) (*embopt.Options, error) {
	if configured == nil || requested == nil {
		return mergeEffectiveMatch(configured, requested), nil
	}
	if len(configured.Inclusions) > 0 && len(requested.Inclusions) > 0 && !slices.Equal(configured.Inclusions, requested.Inclusions) {
		return nil, identity.ErrResourceDenied
	}
	result := mergeEffectiveMatch(configured, requested)
	if result != nil {
		result.Exclusions = append(append([]string(nil), requested.Exclusions...), configured.Exclusions...)
		if configured.MaxFileSize > 0 && (result.MaxFileSize <= 0 || result.MaxFileSize > configured.MaxFileSize) {
			result.MaxFileSize = configured.MaxFileSize
		}
	}
	return result, nil
}

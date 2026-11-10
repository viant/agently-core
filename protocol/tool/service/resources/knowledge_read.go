package resources

import (
	"context"
	"fmt"
	"io/fs"
	neturl "net/url"
	"strings"

	agmodel "github.com/viant/agently-core/protocol/agent"
	identity "github.com/viant/agently-core/protocol/resource"
	scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	"github.com/viant/embedius/matching"
	embopt "github.com/viant/embedius/matching/option"
)

type knowledgeReadRoot struct {
	uri   string
	id    string
	match *embopt.Options
}

func isKnowledgeRoot(uri string) bool {
	return strings.HasPrefix(uri, "knowledge://")
}

func (s *Service) readRoots(ctx context.Context) ([]string, []knowledgeReadRoot) {
	var knowledge []knowledgeReadRoot
	allowed := s.agentAllowedWith(ctx, func(uri string, entry *agmodel.Resource) {
		if isKnowledgeRoot(uri) {
			knowledge = append(knowledge, knowledgeReadRoot{uri: uri, id: strings.TrimSpace(entry.ID), match: entry.Match})
		}
	})
	if len(allowed) == 0 {
		for _, location := range s.defaults.Locations {
			if !isKnowledgeRoot(strings.TrimSpace(location)) {
				continue
			}
			uri, _, err := s.normalizeUserRoot(ctx, location)
			if err == nil && uri != "" {
				knowledge = append(knowledge, knowledgeReadRoot{uri: uri})
			}
		}
	}
	return allowed, knowledge
}

// A Knowledge child is a key within a configured or explicitly selected
// published corpus. Its path cannot select another authority.
func (s *Service) resolveKnowledgeReadTarget(ctx context.Context, input *ReadInput, allowed []string, roots []knowledgeReadRoot) (*readTarget, error) {
	rootURI, rootID := strings.TrimSpace(input.RootURI), strings.TrimSpace(input.RootID)
	child, uri := strings.TrimSpace(input.Path), strings.TrimSpace(input.URI)
	var selected *knowledgeReadRoot
	for i := range roots {
		root := &roots[i]
		if (rootURI != "" && rootURI == root.uri) || (rootURI == "" && rootID != "" && (rootID == root.uri || strings.EqualFold(rootID, root.id))) {
			selected = root
			break
		}
	}
	if selected != nil && rootURI != "" && rootID != "" && rootID != selected.uri && !strings.EqualFold(rootID, selected.id) {
		return nil, identity.ErrResourceDenied
	}
	if selected == nil && (isKnowledgeRoot(rootURI) || isKnowledgeRoot(rootID)) {
		root := rootURI
		if root == "" {
			root = rootID
		}
		if !isKnowledgeRoot(root) || (rootID != "" && rootID != root) || s.augmenter == nil || !s.augmenter.PublishedKnowledgeClaims(root) {
			return nil, identity.ErrResourceDenied
		}
		if _, err := identity.ParseResourceURI(root); err != nil {
			return nil, identity.ErrResourceDenied
		}
		selected = &knowledgeReadRoot{uri: root}
	}
	if uri == "" && (strings.Contains(child, "://") || isAbsLikePath(child)) {
		uri = child
		child = ""
	}
	if uri != "" {
		if !isKnowledgeRoot(uri) {
			if selected != nil {
				return nil, identity.ErrResourceDenied
			}
			return nil, nil
		}
		if selected == nil {
			if rootURI != "" || rootID != "" {
				return nil, identity.ErrResourceDenied
			}
			parent, err := s.publishedKnowledgeParent(ctx, uri)
			if err != nil {
				return nil, err
			}
			selected = &knowledgeReadRoot{uri: parent}
			for i := range roots {
				if roots[i].uri == parent {
					selected = &roots[i]
					break
				}
			}
		}
		if selected == nil || !strings.HasPrefix(uri, selected.uri+"/") {
			return nil, identity.ErrResourceDenied
		}
		child = strings.TrimPrefix(uri, selected.uri+"/")
	} else if selected == nil && rootURI == "" && rootID == "" && len(allowed) == 1 && len(roots) == 1 && allowed[0] == roots[0].uri {
		selected = &roots[0]
	}
	if selected == nil {
		return nil, nil
	}
	if !validKnowledgeChild(child) {
		return nil, identity.ErrResourceDenied
	}
	return &readTarget{fullURI: selected.uri + "/" + child, normRoot: selected.uri, knowledgeRoot: selected, knowledgeChild: child}, nil
}

func validKnowledgeChild(child string) bool {
	if child == "." || !fs.ValidPath(child) || strings.ContainsAny(child, "\\:\x00?#") {
		return false
	}
	decoded, err := neturl.PathUnescape(child)
	return err == nil && decoded != "." && fs.ValidPath(decoded) && !strings.ContainsAny(decoded, "\\:\x00?#") &&
		strings.Count(decoded, "/") == strings.Count(child, "/")
}

func (s *Service) readPublishedKnowledge(ctx context.Context, target *readTarget) ([]byte, error) {
	if s.augmenter == nil {
		return nil, identity.ErrResourceDenied
	}
	if options := target.knowledgeRoot.match; options != nil && matching.New(options.Options()...).IsExcluded(target.fullURI, 0) {
		return nil, identity.ErrResourceDenied
	}
	data, err := s.augmenter.ReadPublishedKnowledgeFile(ctx, target.knowledgeRoot.uri, target.knowledgeChild)
	if err != nil {
		return nil, err
	}
	if options := target.knowledgeRoot.match; options != nil && options.MaxFileSize > 0 && len(data) > options.MaxFileSize {
		return nil, identity.ErrResourceDenied
	}
	if int64(len(data)) > scratchpadsvc.MaxArtifactBytes {
		return nil, fmt.Errorf("resource exceeds input byte limit")
	}
	return data, nil
}

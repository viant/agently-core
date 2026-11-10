package workspace

import (
	"context"
	"sort"

	svcauth "github.com/viant/agently-core/service/auth"
	resources "github.com/viant/agently-core/service/resource"
	primitive "github.com/viant/forge/backend/provider/contract"
)

// NamespaceStarterTasks is present when an admitted namespace configures chat
// suggestions. IDs and category references are qualified by the owning URI.
type NamespaceStarterTasks struct {
	Tasks      []NamespaceStarterTask          `json:"tasks"`
	Categories []primitive.StarterTaskCategory `json:"categories,omitempty"`
}

type NamespaceStarterTask struct {
	primitive.StarterTask
	LookupFingerprint string `json:"lookupFingerprint,omitempty"`
}

func (h *MetadataHandler) SetStarterTaskSources(gateway *resources.Gateway, preferences func(context.Context) (*svcauth.StarterTaskPreferences, error)) {
	h.starterTaskGateway = gateway
	h.starterTaskPreferences = preferences
}

func loadNamespaceStarterTasks(ctx context.Context, gateway *resources.Gateway) (*NamespaceStarterTasks, error) {
	if gateway == nil {
		return nil, nil
	}
	connections := gateway.PublishedMetadataConnections()
	if len(connections) == 0 {
		return nil, nil
	}
	var definitions []resources.Located[primitive.StarterTaskEnvelope]
	query := resources.CatalogQuery{Limit: 100}
	for {
		page, err := resources.ListPageFrom[primitive.StarterTaskEnvelope](ctx, gateway, connections, primitive.StarterTaskKind, query, true)
		if err != nil {
			return nil, err
		}
		for _, row := range page.Items {
			if row.Resource.Name == primitive.StarterTaskResourceName {
				definitions = append(definitions, row)
			}
		}
		if page.Complete {
			break
		}
		query.Cursor = page.NextCursor
	}
	if len(definitions) == 0 {
		return nil, nil
	}
	sort.Slice(definitions, func(i, j int) bool {
		return definitions[i].Resource.URI < definitions[j].Resource.URI
	})
	result := &NamespaceStarterTasks{Tasks: []NamespaceStarterTask{}}
	for _, row := range definitions {
		if err := row.Definition.Validate(); err != nil {
			return nil, err
		}
		prefix := row.Resource.URI + "#"
		for _, task := range row.Definition.Tasks {
			task.ID = prefix + task.ID
			if task.CategoryID != "" {
				task.CategoryID = prefix + task.CategoryID
			}
			projection := NamespaceStarterTask{StarterTask: task}
			if len(task.LookupLibraries) > 0 {
				projection.LookupFingerprint = row.Resource.ContentFingerprint
				projection.LookupLibraries = nil
			}
			result.Tasks = append(result.Tasks, projection)
		}
		for _, category := range row.Definition.Categories {
			category.ID = prefix + category.ID
			result.Categories = append(result.Categories, category)
		}
	}
	sort.SliceStable(result.Tasks, func(i, j int) bool {
		return result.Tasks[i].Priority > result.Tasks[j].Priority
	})
	return result, nil
}

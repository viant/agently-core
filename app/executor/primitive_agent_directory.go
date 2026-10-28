package executor

import (
	"context"
	"encoding/json"

	llmagents "github.com/viant/agently-core/protocol/tool/service/llm/agents"
	resources "github.com/viant/agently-core/service/resource"
)

func publishedAgentDirectory(runtime *Runtime, native func() []llmagents.ListItem) func(context.Context) ([]llmagents.ListItem, error) {
	return func(ctx context.Context) ([]llmagents.ListItem, error) {
		var result []llmagents.ListItem
		if native != nil {
			result = append(result, native()...)
		}
		if runtime.PrimitiveProviders == nil {
			return result, nil
		}
		roots := runtime.PrimitiveProviders.PublishedMetadataConnections()
		if len(roots) == 0 {
			return result, nil
		}
		query := resources.CatalogQuery{Limit: 100}
		for {
			page, err := resources.ListPageFrom[json.RawMessage](ctx, runtime.PrimitiveProviders, roots, "agent", query)
			if err != nil {
				return nil, err
			}
			for _, row := range page.Items {
				result = append(result, llmagents.ListItem{ID: row.Resource.URI, Name: row.Resource.Title, Description: row.Resource.Description, Source: "internal"})
			}
			if page.Complete {
				return result, nil
			}
			query.Cursor = page.NextCursor
		}
	}
}

package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/viant/agently-core/protocol/agui/extensions"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/sdk/api"
)

// dispatchAGUIWorkspace maps deterministic typed domain operations. The outer
// run supplies authentication, request identity, journal boundaries and target.
// Filesystem mutations are not claimed to participate in DB rollback.
func dispatchAGUIWorkspace(ctx context.Context, client Client, threadID, operation string, payload json.RawMessage) (any, bool, error) {
	switch operation {
	case "workspace.metadata.get", "workspace.publicagents.list", "workspace.layout.get", "workspace.tools.list", "workspace.models.list", "workspace.model.get", "workspace.model.save", "datasource.fetch", "datasource.cache.invalidate", "lookup.registry", "workspace.resource.list", "workspace.resource.get", "workspace.resource.save", "workspace.resource.delete", "workspace.resource.export", "workspace.resource.import", "feed.list", "feed.get":
	default:
		return nil, false, nil
	}
	if client == nil {
		return nil, true, fmt.Errorf("workspace client is required")
	}
	if err := extensions.ValidateWorkspacePayload(operation, payload); err != nil {
		return nil, true, err
	}

	if strings.HasPrefix(operation, "workspace.resource.") {
		var check struct {
			Kind, Name string
			Kinds      []string
			Resources  []struct{ Kind, Name string }
		}
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &check); err != nil {
				return nil, true, err
			}
		}
		for _, value := range append([]string{check.Kind, check.Name}, check.Kinds...) {
			if value != "" && !safeAGUIResourcePath(value) {
				return nil, true, fmt.Errorf("invalid workspace resource path")
			}
		}
		for _, resource := range check.Resources {
			if !safeAGUIResourcePath(resource.Kind) || !safeAGUIResourcePath(resource.Name) {
				return nil, true, fmt.Errorf("invalid workspace resource path")
			}
		}
	}
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	decode := func(target any) error {
		decoder := json.NewDecoder(strings.NewReader(string(payload)))
		decoder.UseNumber()
		return decoder.Decode(target)
	}
	switch operation {
	case "workspace.metadata.get", "workspace.publicagents.list", "workspace.layout.get", "workspace.tools.list", "workspace.models.list", "workspace.model.get", "workspace.model.save":
		result, err := dispatchAGUIWorkspaceMetadata(ctx, operation, payload)
		return result, true, err
	case "datasource.fetch":
		backend, ok := client.(Backend)
		if !ok {
			return nil, true, ErrDatasourceStackNotConfigured
		}
		var input api.FetchDatasourceInput
		if err := decode(&input); err != nil {
			return nil, true, err
		}
		if strings.TrimSpace(threadID) == "" {
			return nil, true, fmt.Errorf("datasource threadId is required")
		}
		if err := authorizeDatasourceConversation(ctx, backend, threadID); err != nil {
			return nil, true, err
		}
		input.ConversationID = threadID
		result, err := backend.FetchDatasource(requestctx.WithConversationID(ctx, threadID), &input)
		return result, true, err
	case "datasource.cache.invalidate":
		backend, ok := client.(Backend)
		if !ok {
			return nil, true, ErrDatasourceStackNotConfigured
		}
		var input api.InvalidateDatasourceCacheInput
		if err := decode(&input); err != nil {
			return nil, true, err
		}
		if err := backend.InvalidateDatasourceCache(ctx, &input); err != nil {
			return nil, true, err
		}
		return map[string]any{"invalidated": true}, true, nil
	case "lookup.registry":
		backend, ok := client.(Backend)
		if !ok {
			return nil, true, ErrDatasourceStackNotConfigured
		}
		var input api.ListLookupRegistryInput
		if err := decode(&input); err != nil {
			return nil, true, err
		}
		result, err := backend.ListLookupRegistry(ctx, &input)
		return result, true, err
	case "workspace.resource.list":
		var input struct {
			Kind string `json:"kind"`
		}
		if err := decode(&input); err != nil {
			return nil, true, err
		}
		result, err := client.ListResources(ctx, &ListResourcesInput{Kind: input.Kind})
		return result, true, err
	case "workspace.resource.get", "workspace.resource.delete":
		var input ResourceRef
		if err := decode(&input); err != nil {
			return nil, true, err
		}
		if operation == "workspace.resource.delete" {
			if err := client.DeleteResource(ctx, &input); err != nil {
				return nil, true, err
			}
			return map[string]any{"deleted": true}, true, nil
		}
		result, err := client.GetResource(ctx, &input)
		return result, true, err
	case "workspace.resource.save":
		var input struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
			Data []byte `json:"data"`
		}
		if err := decode(&input); err != nil {
			return nil, true, err
		}
		if err := client.SaveResource(ctx, &SaveResourceInput{Kind: input.Kind, Name: input.Name, Data: input.Data}); err != nil {
			return nil, true, err
		}
		return map[string]any{"saved": true}, true, nil
	case "workspace.resource.export":
		var input ExportResourcesInput
		if err := decode(&input); err != nil {
			return nil, true, err
		}
		result, err := client.ExportResources(ctx, &input)
		return result, true, err
	case "workspace.resource.import":
		var input ImportResourcesInput
		if err := decode(&input); err != nil {
			return nil, true, err
		}
		result, err := client.ImportResources(ctx, &input)
		return result, true, err
	case "feed.list":
		backend, ok := client.(interface{ ListFeedSpecs() []*FeedSpec })
		if !ok {
			return nil, true, fmt.Errorf("feed registry is not configured")
		}
		type summary struct {
			ID            string            `json:"id"`
			Title         string            `json:"title"`
			DeveloperOnly bool              `json:"developerOnly,omitempty"`
			Presentation  *FeedPresentation `json:"presentation,omitempty"`
			Match         FeedMatch         `json:"match"`
		}
		results := make([]summary, 0)
		for _, spec := range backend.ListFeedSpecs() {
			if spec != nil {
				results = append(results, summary{spec.ID, spec.Title, spec.DeveloperOnly, normalizedFeedPresentation(spec), spec.Match})
			}
		}
		return map[string]any{"feeds": results}, true, nil
	case "feed.get":
		var input struct {
			ID string `json:"id"`
		}
		if err := decode(&input); err != nil {
			return nil, true, err
		}
		backend, ok := client.(interface{ ListFeedSpecs() []*FeedSpec })
		if !ok {
			return nil, true, fmt.Errorf("feed registry is not configured")
		}
		var spec *FeedSpec
		for _, candidate := range backend.ListFeedSpecs() {
			if candidate != nil && candidate.ID == input.ID {
				spec = candidate
				break
			}
		}
		if spec == nil {
			return nil, true, fmt.Errorf("feed %q not found", input.ID)
		}
		transcript, err := client.GetTranscript(ctx, &GetTranscriptInput{ConversationID: threadID, IncludeModelCalls: true, IncludeToolCalls: true}, WithIncludeFeeds())
		if err != nil {
			return nil, true, err
		}
		var data any
		if transcript != nil {
			for _, feed := range transcript.Feeds {
				if feed != nil && feed.FeedID == spec.ID {
					data = feed.Data
					break
				}
			}
			if data == nil && transcript.Conversation != nil {
				for _, feed := range transcript.Conversation.Feeds {
					if feed != nil && feed.FeedID == spec.ID {
						data = feed.Data
						break
					}
				}
			}
		}
		return map[string]any{"feedId": spec.ID, "title": spec.Title, "developerOnly": spec.DeveloperOnly, "presentation": normalizedFeedPresentation(spec), "data": normalizeFeedResponseData(data), "dataSources": normalizeFeedConfigValue(spec.DataSource), "ui": normalizeFeedConfigValue(spec.UI)}, true, nil
	}
	return nil, true, fmt.Errorf("unreachable workspace operation")
}

func safeAGUIResourcePath(value string) bool {
	if strings.TrimSpace(value) == "" || filepath.IsAbs(value) || strings.ContainsAny(value, "\\\x00") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

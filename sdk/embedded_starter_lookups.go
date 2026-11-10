package sdk

import (
	"context"
	"encoding/json"
	"strings"

	loproto "github.com/viant/agently-core/protocol/lookup/overlay"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/sdk/api"
	overlays "github.com/viant/agently-core/service/lookup/overlay"
	primitive "github.com/viant/forge/backend/provider/contract"
	resourcehost "github.com/viant/forge/backend/resourcehost"
)

func (c *backendClient) starterLookupEntries(ctx context.Context, in *api.ListLookupRegistryInput, kind, agent string) ([]loproto.RegistryEntry, error) {
	if in.StarterTaskID == "" && in.StarterTaskFingerprint == "" {
		return nil, nil
	}
	set, id, ok := strings.Cut(in.StarterTaskID, "#")
	uri, err := identity.ParseResourceURI(set)
	if !ok || err != nil || uri.Kind != primitive.StarterTaskKind || uri.Name != primitive.StarterTaskResourceName || id == "" || kind != "chat-composer" {
		return nil, identity.ErrResourceDenied
	}
	loaded, err := resourcehost.Load(ctx, c.primitiveProviders, set, resourcehost.PublishedCodec[map[string]primitive.StarterTask]{
		ID: "agently.startertask.lookup.v1", Immutable: true, Decode: func(_ context.Context, _ string, raw json.RawMessage) (map[string]primitive.StarterTask, error) {
			var definition primitive.StarterTaskEnvelope
			if err := json.Unmarshal(raw, &definition); err != nil {
				return nil, err
			}
			if err := definition.Validate(); err != nil {
				return nil, err
			}
			tasks := make(map[string]primitive.StarterTask, len(definition.Tasks))
			for _, task := range definition.Tasks {
				tasks[task.ID] = task
			}
			return tasks, nil
		},
	})
	if err != nil {
		return nil, err
	}
	if loaded.Resource.ContentFingerprint != in.StarterTaskFingerprint {
		return nil, identity.ErrResourceStale
	}
	task, ok := loaded.Value[id]
	if !ok || len(task.LookupLibraries) == 0 || (task.AgentID != "" && task.AgentID != agent) {
		return nil, identity.ErrResourceDenied
	}
	service, err := overlays.ReadStarterLibraries(ctx, c.primitiveProviders, *loaded.Resource, task.LookupLibraries)
	if err != nil {
		return nil, err
	}
	entries, err := service.RegistryScoped(kind, agent)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, identity.ErrResourceDenied
	}
	for _, entry := range entries {
		if entry.PickerResource == nil || entry.WindowId != entry.PickerResource.URI || entry.ProviderIdentity != loaded.Resource.ProviderIdentity {
			return nil, identity.ErrResourceDenied
		}
		picker, err := identity.ParseResourceURI(entry.WindowId)
		if err != nil || picker.Kind != "window" || picker.Namespace != uri.Namespace {
			return nil, identity.ErrResourceDenied
		}
		opened, err := resourcehost.Load(ctx, c.primitiveProviders, picker.String(), resourcehost.PublishedCodec[struct{}]{
			ID: "agently.startertask.picker-admission.v1", Immutable: true,
			Decode: func(context.Context, string, json.RawMessage) (struct{}, error) { return struct{}{}, nil },
		})
		if err != nil {
			return nil, err
		}
		if opened.Resource.ProviderIdentity != entry.ProviderIdentity {
			return nil, identity.ErrResourceDenied
		}
	}
	if err := c.primitiveProviders.VerifyPublishedMetadata(ctx, *loaded.Resource); err != nil {
		return nil, err
	}
	return entries, nil
}

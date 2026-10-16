package sdk

import (
	"context"
	"encoding/json"
	"errors"
	identity "github.com/viant/agently-core/protocol/resource"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ApplyPermissionInput identifies one concrete window instance whose authored
// metadata should be compiled for the authenticated principal after the
// resource has been returned by an ACL-protected datasource.
type ApplyPermissionInput struct {
	ResolvedResource           *identity.ResolvedResource
	ConversationID             string
	AuthorizationSchemaVersion int // optional; 1 numeric IDs or 2 exact string IDs
	Resource                   map[string]interface{}
	WindowParams               map[string]interface{}
	Target                     *MetadataTargetContext
}

// ApplyPermission compiles authored Forge metadata into a permitted view and
// returns the resource-specific tree. Call GetForgeWindowMetadata for the
// complete authored document. Datasource fetch contracts remain unchanged.
func (c *HTTPClient) ApplyPermission(ctx context.Context, windowKey string, input *ApplyPermissionInput) (json.RawMessage, error) {
	windowKey = strings.TrimSpace(windowKey)
	if windowKey == "" {
		return nil, errors.New("window key is required")
	}
	if input == nil {
		return nil, errors.New("apply permission input is required")
	}
	path := appendMetadataTargetQuery("/v1/api/agently/forge/window/"+url.PathEscape(windowKey), input.Target)
	parsed, err := url.Parse(path)
	if err != nil {
		return nil, err
	}
	query := parsed.Query()
	query.Set("applyPermission", "true")
	if input.ResolvedResource != nil {
		raw, err := json.Marshal(input.ResolvedResource)
		if err != nil {
			return nil, err
		}
		query.Set("resolvedResource", string(raw))
	}
	if input.AuthorizationSchemaVersion != 0 {
		if input.AuthorizationSchemaVersion != 1 && input.AuthorizationSchemaVersion != 2 {
			return nil, errors.New("authorization schema version must be 1 or 2")
		}
		query.Set("authorizationSchemaVersion", strconv.Itoa(input.AuthorizationSchemaVersion))
	}
	if conversationID := strings.TrimSpace(input.ConversationID); conversationID != "" {
		query.Set("conversationId", conversationID)
	}
	if len(input.Resource) > 0 {
		resource, marshalErr := json.Marshal(input.Resource)
		if marshalErr != nil {
			return nil, marshalErr
		}
		query.Set("resource", string(resource))
	}
	if input.WindowParams != nil {
		encoded, marshalErr := json.Marshal(input.WindowParams)
		if marshalErr != nil {
			return nil, marshalErr
		}
		query.Set("windowParams", string(encoded))
	}
	parsed.RawQuery = query.Encode()
	var raw json.RawMessage
	if err := c.doJSON(ctx, http.MethodGet, parsed.String(), nil, &raw); err != nil {
		return nil, err
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) == nil {
		if data, ok := envelope["data"]; ok {
			return data, nil
		}
	}
	return raw, nil
}

package tool

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"mime"
	"strings"

	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	scratchpad "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	schema "github.com/viant/mcp-protocol/schema"
)

func (r *Registry) outputArtifactPolicy(ctx context.Context, server, method string) (*mcpcfg.OutputArtifact, error) {
	if r.mgr == nil || r.isInternalServer(server) {
		return nil, nil
	}
	options, err := r.mgr.Options(ctx, server)
	if err != nil {
		return nil, errors.New("output artifact policy unavailable")
	}
	if options == nil {
		return nil, nil
	}
	policy, ok := options.OutputArtifacts[method]
	if !ok {
		return nil, nil
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(authctx.EffectiveUserID(ctx)) == "" {
		return nil, errors.New("output artifact identity required")
	}
	return &policy, nil
}

// captureOutputArtifacts is called before result serialization, app recording,
// errors, caches or transcript handling. Original content is never returned.
func captureOutputArtifacts(ctx context.Context, result *schema.CallToolResult, policy *mcpcfg.OutputArtifact) (*schema.CallToolResult, error) {
	failure := func() (*schema.CallToolResult, error) { return nil, errors.New("output artifact capture failed") }
	if policy == nil || policy.Validate() != nil || strings.TrimSpace(authctx.EffectiveUserID(ctx)) == "" {
		return failure()
	}
	if result == nil || result.ResultType == schema.ResultTypeInputRequired || (result.IsError != nil && *result.IsError) || len(result.Content) > 32 {
		return failure()
	}
	type pending struct {
		data []byte
		mime string
	}
	var blobs []pending
	var total int64
	for _, content := range result.Content {
		var resource *schema.EmbeddedResourceResource
		switch value := content.(type) {
		case schema.EmbeddedResource:
			resource = &value.Resource
		case *schema.EmbeddedResource:
			if value != nil {
				resource = &value.Resource
			}
		case schema.TextContent, *schema.TextContent:
			// Discard text duplicates, including legacy binary text echoes.
			continue
		case map[string]interface{}:
			if value["type"] == "text" {
				continue
			}
			if value["type"] != "resource" {
				return failure()
			}
			object, ok := value["resource"].(map[string]interface{})
			if !ok {
				return failure()
			}
			blob, ok := object["blob"].(string)
			if !ok {
				return failure()
			}
			uri, ok := object["uri"].(string)
			if !ok {
				return failure()
			}
			resource = &schema.EmbeddedResourceResource{Uri: uri, Blob: blob}
			if raw, present := object["text"]; present {
				text, ok := raw.(string)
				if !ok || text != "" {
					return failure()
				}
			}
			if raw := object["mimeType"]; raw != nil {
				mediaType, ok := raw.(string)
				if !ok {
					return failure()
				}
				resource.MimeType = &mediaType
			}
		default:
			return failure() // ResourceLink does not grant URL access.
		}
		if resource == nil || resource.Blob == "" || resource.Text != "" || resource.Uri == "" {
			return failure()
		}
		mediaType := "application/octet-stream"
		if resource.MimeType != nil {
			var err error
			mediaType, _, err = mime.ParseMediaType(*resource.MimeType)
			if err != nil || mediaType == "" || len(*resource.MimeType) > 256 {
				return failure()
			}
		}
		remaining := policy.ByteLimit() - total
		if int64(len(resource.Blob)) > ((remaining+2)/3)*4 {
			return failure()
		}
		data, err := base64.StdEncoding.Strict().DecodeString(resource.Blob)
		if err != nil || len(data) == 0 || int64(len(data)) > remaining {
			return failure()
		}
		total += int64(len(data))
		blobs = append(blobs, pending{data: data, mime: mediaType})
	}
	if len(blobs) == 0 {
		return failure()
	}
	resources := make([]map[string]interface{}, 0, len(blobs))
	name := policy.Name
	if name == "" {
		name = "artifact"
	}
	for _, blob := range blobs {
		// Wire resource URIs are untrusted metadata and may contain payload
		// echoes or credentials. Never persist them as public provenance.
		d, err := scratchpad.New().PublishArtifact(ctx, "", name, blob.mime, "", bytes.NewReader(blob.data))
		if err != nil {
			return failure()
		}
		resources = append(resources, map[string]interface{}{"id": d.ID, "uri": d.URI, "name": d.Name, "mimeType": d.MimeType, "sizeBytes": d.SizeBytes, "sha256": d.SHA256, "downloadURI": "/v1/artifacts/" + d.ID})
	}
	return &schema.CallToolResult{StructuredContent: map[string]interface{}{"resources": resources}}, nil
}

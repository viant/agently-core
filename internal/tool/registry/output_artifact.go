package tool

import (
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
		encoded string
		mime    string
	}
	var blobs []pending
	name := policy.Name
	if policy.NamePath != "" {
		value, found, valid := artifactPointerOptionalString(result, policy.NamePath)
		if !valid {
			return failure()
		}
		if found && value != "" {
			name = value
		}
	}
	if name == "" {
		name = "artifact"
	}
	if (mcpcfg.OutputArtifact{Name: name}).Validate() != nil {
		return failure()
	}
	declaredMIME := policy.MimeType
	if policy.MimeTypePath != "" {
		value, found, valid := artifactPointerOptionalString(result, policy.MimeTypePath)
		if !valid {
			return failure()
		}
		if found && value != "" {
			declaredMIME = value
		}
		if declaredMIME != "" {
			if _, _, err := mime.ParseMediaType(declaredMIME); err != nil {
				return failure()
			}
		}
	}
	contents := result.Content
	if policy.BytesPath != "" {
		encoded, ok := artifactPointerString(result, policy.BytesPath)
		if !ok || encoded == "" {
			return failure()
		}
		mediaType := declaredMIME
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		blobs = append(blobs, pending{encoded: encoded, mime: mediaType})
		contents = nil
	}
	for _, content := range contents {
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
		mediaType := declaredMIME
		if resource.MimeType != nil && *resource.MimeType != "" {
			mediaType = *resource.MimeType
		}
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		blobs = append(blobs, pending{encoded: resource.Blob, mime: mediaType})
	}
	if len(blobs) == 0 {
		return failure()
	}
	resources := make([]map[string]interface{}, 0, len(blobs))
	for _, blob := range blobs {
		if len(blob.mime) > 256 || artifactMetadataEcho(name, blob.encoded) || artifactMetadataEcho(blob.mime, blob.encoded) {
			return failure()
		}
		mediaType, _, err := mime.ParseMediaType(blob.mime)
		if err != nil || mediaType == "" {
			return failure()
		}
		// Wire resource URIs are untrusted metadata and may contain payload
		// echoes or credentials. Never persist them as public provenance.
		d, err := scratchpad.New().PublishArtifactStream(ctx, name, mediaType, "", base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(blob.encoded)))
		if err != nil {
			return failure()
		}
		resources = append(resources, map[string]interface{}{"id": d.ID, "uri": d.URI, "name": d.Name, "mimeType": d.MimeType, "sizeBytes": d.SizeBytes, "sha256": d.SHA256, "downloadURI": "/v1/artifacts/" + d.ID})
	}
	return &schema.CallToolResult{StructuredContent: map[string]interface{}{"resources": resources}}, nil
}

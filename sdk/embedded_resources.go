package sdk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	authctx "github.com/viant/agently-core/internal/auth"
	scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"

	"github.com/google/uuid"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/workspace"
)

func (c *backendClient) UploadFile(ctx context.Context, input *UploadFileInput) (*UploadFileOutput, error) {
	if c.conv == nil {
		return nil, errors.New("conversation client not configured")
	}
	if input == nil || strings.TrimSpace(input.ConversationID) == "" {
		return nil, errors.New("conversation ID is required")
	}
	resourceURI := strings.TrimSpace(input.ResourceURI)
	hasData := len(input.Data) > 0
	if hasData == (resourceURI != "") {
		return nil, errors.New("exactly one of file data or resource URI is required")
	}
	if resourceURI != "" {
		artifactID, err := scratchpadsvc.ArtifactID(resourceURI)
		if err != nil {
			return nil, err
		}
		descriptor, reader, err := scratchpadsvc.New().OpenArtifact(ctx, resourceURI)
		if err != nil {
			return nil, fmt.Errorf("opening scratchpad artifact: %w", err)
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, scratchpadsvc.MaxArtifactBytes+1))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, fmt.Errorf("reading scratchpad artifact: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("closing scratchpad artifact: %w", closeErr)
		}
		if int64(len(data)) > scratchpadsvc.MaxArtifactBytes {
			return nil, fmt.Errorf("artifact exceeds %d bytes", scratchpadsvc.MaxArtifactBytes)
		}
		if int64(len(data)) != descriptor.SizeBytes {
			return nil, fmt.Errorf("artifact size mismatch")
		}
		return c.storeConversationFile(ctx, conversationFileInput{
			ConversationID: strings.TrimSpace(input.ConversationID),
			Name:           descriptor.Name,
			ContentType:    descriptor.MimeType,
			Data:           data,
			Provider:       "scratchpad",
			ProviderFileID: artifactID,
			Checksum:       descriptor.SHA256,
			Resource:       descriptor,
		})
	}

	return c.storeConversationFile(ctx, conversationFileInput{
		ConversationID: strings.TrimSpace(input.ConversationID),
		Name:           input.Name,
		ContentType:    input.ContentType,
		Data:           input.Data,
		Provider:       "upload",
		Publish:        authctx.EffectiveUserID(ctx) != "",
	})
}

type conversationFileInput struct {
	ConversationID string
	Name           string
	ContentType    string
	Data           []byte
	Provider       string
	ProviderFileID string
	Checksum       string
	Resource       *scratchpadsvc.ArtifactDescriptor
	Publish        bool
}

func (c *backendClient) storeConversationFile(ctx context.Context, input conversationFileInput) (*UploadFileOutput, error) {
	if len(input.Data) == 0 {
		return nil, errors.New("file data is required")
	}

	fileID := uuid.New().String()
	payloadID := uuid.New().String()

	// Store the file content as an inline payload.
	contentType := strings.TrimSpace(input.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	p := conversation.NewPayload()
	p.SetId(payloadID)
	p.SetKind("attachment")
	p.SetStorage("inline")
	p.SetInlineBody(input.Data)
	p.SetSizeBytes(len(input.Data))
	p.SetMimeType(contentType)
	if err := c.conv.PatchPayload(ctx, p); err != nil {
		return nil, fmt.Errorf("storing file payload: %w", err)
	}

	// Register the file in the generated-file index when the conversation
	// client supports it (datly-backed and memory implementations do).
	if gfc, ok := c.conv.(conversation.GeneratedFileClient); ok {
		now := time.Now().UTC()
		gf := conversation.NewGeneratedFile()
		gf.SetID(fileID)
		gf.SetConversationID(input.ConversationID)
		gf.SetPayloadID(payloadID)
		gf.SetMode("inline")
		gf.SetCopyMode("eager")
		gf.SetStatus("ready")
		gf.SetProvider(input.Provider)
		if providerFileID := strings.TrimSpace(input.ProviderFileID); providerFileID != "" {
			gf.SetProviderFileID(providerFileID)
		}
		if name := strings.TrimSpace(input.Name); name != "" {
			gf.SetFilename(name)
		}
		gf.SetMimeType(contentType)
		gf.SetSizeBytes(len(input.Data))
		if checksum := strings.TrimSpace(input.Checksum); checksum != "" {
			gf.SetChecksum(checksum)
		}
		gf.SetCreatedAt(now)
		gf.SetUpdatedAt(now)
		if err := gfc.PatchGeneratedFile(ctx, gf); err != nil {
			return nil, fmt.Errorf("registering uploaded file: %w", err)
		}
	}

	out := &UploadFileOutput{Resource: input.Resource, ID: fileID, Name: input.Name, Size: int64(len(input.Data)), MimeType: contentType}
	// Anonymous legacy clients retain their old contract; user-scoped resource
	// publication requires authenticated identity, never an invented shared user.
	if input.Publish {
		d, err := scratchpadsvc.New().PublishArtifact(ctx, fileID, input.Name, contentType, "", bytes.NewReader(input.Data))
		if err != nil {
			return nil, err
		}
		out.Resource = d
	}
	return out, nil
}

func (c *backendClient) DownloadFile(ctx context.Context, input *DownloadFileInput) (*DownloadFileOutput, error) {
	if c.data == nil {
		return nil, errors.New("data service not configured")
	}
	if input == nil || strings.TrimSpace(input.ConversationID) == "" || strings.TrimSpace(input.FileID) == "" {
		return nil, errors.New("conversation ID and file ID are required")
	}
	rows, err := c.data.ListGeneratedFiles(ctx, input.ConversationID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row == nil || strings.TrimSpace(row.ID) != strings.TrimSpace(input.FileID) || row.PayloadID == nil || strings.TrimSpace(*row.PayloadID) == "" {
			continue
		}
		payload, err := c.GetPayload(ctx, strings.TrimSpace(*row.PayloadID))
		if err != nil {
			return nil, err
		}
		if payload == nil {
			return nil, nil
		}
		out := &DownloadFileOutput{}
		if row.Filename != nil {
			out.Name = *row.Filename
		}
		if row.MimeType != nil {
			out.ContentType = *row.MimeType
		}
		if payload.InlineBody != nil {
			out.Data = make([]byte, len(*payload.InlineBody))
			copy(out.Data, *payload.InlineBody)
		}
		return out, nil
	}
	return nil, nil
}

func (c *backendClient) ListFiles(ctx context.Context, input *ListFilesInput) (*ListFilesOutput, error) {
	if c.data == nil {
		return nil, errors.New("data service not configured")
	}
	if input == nil || strings.TrimSpace(input.ConversationID) == "" {
		return nil, errors.New("conversation ID is required")
	}
	rows, err := c.data.ListGeneratedFiles(ctx, input.ConversationID)
	if err != nil {
		return nil, err
	}
	out := &ListFilesOutput{}
	for _, r := range rows {
		entry := &FileEntry{ID: r.ID}
		if r.Filename != nil {
			entry.Name = *r.Filename
		}
		if r.MimeType != nil {
			entry.ContentType = *r.MimeType
		}
		if r.SizeBytes != nil {
			entry.Size = int64(*r.SizeBytes)
		}
		out.Files = append(out.Files, entry)
	}
	return out, nil
}

func (c *backendClient) ListResources(ctx context.Context, input *ListResourcesInput) (*ListResourcesOutput, error) {
	if c.store == nil {
		return nil, errors.New("workspace store not configured")
	}
	if input == nil || strings.TrimSpace(input.Kind) == "" {
		return nil, errors.New("resource kind is required")
	}
	names, err := c.store.List(ctx, input.Kind)
	if err != nil {
		return nil, err
	}
	return &ListResourcesOutput{Names: names}, nil
}

func (c *backendClient) GetResource(ctx context.Context, input *ResourceRef) (*GetResourceOutput, error) {
	if c.store == nil {
		return nil, errors.New("workspace store not configured")
	}
	if input == nil || strings.TrimSpace(input.Kind) == "" || strings.TrimSpace(input.Name) == "" {
		return nil, errors.New("resource kind and name are required")
	}
	data, err := c.store.Load(ctx, input.Kind, input.Name)
	if err != nil {
		return nil, err
	}
	return &GetResourceOutput{Kind: input.Kind, Name: input.Name, Data: data}, nil
}

func (c *backendClient) SaveResource(ctx context.Context, input *SaveResourceInput) error {
	if c.store == nil {
		return errors.New("workspace store not configured")
	}
	if input == nil || strings.TrimSpace(input.Kind) == "" || strings.TrimSpace(input.Name) == "" {
		return errors.New("resource kind and name are required")
	}
	return c.store.Save(ctx, input.Kind, input.Name, input.Data)
}

func (c *backendClient) DeleteResource(ctx context.Context, input *ResourceRef) error {
	if c.store == nil {
		return errors.New("workspace store not configured")
	}
	if input == nil || strings.TrimSpace(input.Kind) == "" || strings.TrimSpace(input.Name) == "" {
		return errors.New("resource kind and name are required")
	}
	return c.store.Delete(ctx, input.Kind, input.Name)
}

func (c *backendClient) ExportResources(ctx context.Context, input *ExportResourcesInput) (*ExportResourcesOutput, error) {
	if c.store == nil {
		return nil, errors.New("workspace store not configured")
	}
	kinds := input.Kinds
	if len(kinds) == 0 {
		kinds = workspace.AllKinds()
	}
	out := &ExportResourcesOutput{}
	for _, kind := range kinds {
		names, err := c.store.List(ctx, kind)
		if err != nil {
			continue
		}
		for _, name := range names {
			data, err := c.store.Load(ctx, kind, name)
			if err != nil {
				continue
			}
			out.Resources = append(out.Resources, Resource{Kind: kind, Name: name, Data: data})
		}
	}
	return out, nil
}

func (c *backendClient) ImportResources(ctx context.Context, input *ImportResourcesInput) (*ImportResourcesOutput, error) {
	if c.store == nil {
		return nil, errors.New("workspace store not configured")
	}
	if input == nil {
		return nil, errors.New("input is required")
	}
	out := &ImportResourcesOutput{}
	for _, r := range input.Resources {
		if strings.TrimSpace(r.Kind) == "" || strings.TrimSpace(r.Name) == "" {
			continue
		}
		if !input.Replace {
			exists, err := c.store.Exists(ctx, r.Kind, r.Name)
			if err == nil && exists {
				out.Skipped++
				continue
			}
		}
		if err := c.store.Save(ctx, r.Kind, r.Name, r.Data); err != nil {
			continue
		}
		out.Imported++
	}
	return out, nil
}

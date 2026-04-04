package sdk

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/workspace"
)

func (c *EmbeddedClient) UploadFile(ctx context.Context, input *UploadFileInput) (*UploadFileOutput, error) {
	if c.conv == nil {
		return nil, errors.New("conversation client not configured")
	}
	if input == nil || strings.TrimSpace(input.ConversationID) == "" {
		return nil, errors.New("conversation ID is required")
	}
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
		gf.SetConversationID(strings.TrimSpace(input.ConversationID))
		gf.SetPayloadID(payloadID)
		gf.SetMode("inline")
		gf.SetCopyMode("eager")
		gf.SetStatus("ready")
		gf.SetProvider("upload")
		if name := strings.TrimSpace(input.Name); name != "" {
			gf.SetFilename(name)
		}
		gf.SetMimeType(contentType)
		gf.SetSizeBytes(len(input.Data))
		gf.SetCreatedAt(now)
		gf.SetUpdatedAt(now)
		if err := gfc.PatchGeneratedFile(ctx, gf); err != nil {
			return nil, fmt.Errorf("registering uploaded file: %w", err)
		}
	}

	return &UploadFileOutput{ID: fileID}, nil
}

func (c *EmbeddedClient) DownloadFile(ctx context.Context, input *DownloadFileInput) (*DownloadFileOutput, error) {
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

func (c *EmbeddedClient) ListFiles(ctx context.Context, input *ListFilesInput) (*ListFilesOutput, error) {
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

func (c *EmbeddedClient) ListResources(ctx context.Context, input *ListResourcesInput) (*ListResourcesOutput, error) {
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

func (c *EmbeddedClient) GetResource(ctx context.Context, input *ResourceRef) (*GetResourceOutput, error) {
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

func (c *EmbeddedClient) SaveResource(ctx context.Context, input *SaveResourceInput) error {
	if c.store == nil {
		return errors.New("workspace store not configured")
	}
	if input == nil || strings.TrimSpace(input.Kind) == "" || strings.TrimSpace(input.Name) == "" {
		return errors.New("resource kind and name are required")
	}
	return c.store.Save(ctx, input.Kind, input.Name, input.Data)
}

func (c *EmbeddedClient) DeleteResource(ctx context.Context, input *ResourceRef) error {
	if c.store == nil {
		return errors.New("workspace store not configured")
	}
	if input == nil || strings.TrimSpace(input.Kind) == "" || strings.TrimSpace(input.Name) == "" {
		return errors.New("resource kind and name are required")
	}
	return c.store.Delete(ctx, input.Kind, input.Name)
}

func (c *EmbeddedClient) ExportResources(ctx context.Context, input *ExportResourcesInput) (*ExportResourcesOutput, error) {
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

func (c *EmbeddedClient) ImportResources(ctx context.Context, input *ImportResourcesInput) (*ImportResourcesOutput, error) {
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

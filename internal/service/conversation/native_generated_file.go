package conversation

import (
	"context"
	"fmt"
	"strings"

	read "github.com/viant/agently-core/internal/datly/generatedfile/read"
	write "github.com/viant/agently-core/internal/datly/generatedfile/write"
	store "github.com/viant/agently-core/internal/store/conversation"
	generatedfilemodel "github.com/viant/agently-core/model/generatedfile"
)

func (s *Service) getGeneratedFilesNative(ctx context.Context, input *generatedfilemodel.Input) ([]*generatedfilemodel.GeneratedFileView, error) {
	query := &read.Input{}
	if input != nil && input.Has != nil {
		h := input.Has
		if h.ConversationID {
			query.SetConversationID(input.ConversationID)
		}
		if h.TurnID {
			query.SetTurnID(input.TurnID)
		}
		if h.MessageID {
			query.SetMessageID(input.MessageID)
		}
		if h.ID {
			query.SetID(input.ID)
		}
		if h.Provider {
			query.SetProvider(input.Provider)
		}
		if h.Status {
			query.SetStatus(input.Status)
		}
		if h.Since {
			query.SetSince(input.Since)
		}
	}
	rows, err := (&store.GeneratedFileStore{Invoker: s.native}).List(ctx, query)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Service) patchGeneratedFileNative(ctx context.Context, file *generatedfilemodel.GeneratedFile) error {
	if file == nil {
		return fmt.Errorf("generated file mutation is required")
	}
	row := &write.GeneratedFile{}
	row.SetId(strings.TrimSpace(file.ID))
	if h := file.Has; h != nil {
		if h.ConversationID {
			row.SetConversationId(file.ConversationID)
		}
		if h.TurnID {
			row.SetTurnId(file.TurnID)
		}
		if h.MessageID {
			row.SetMessageId(file.MessageID)
		}
		if h.Provider {
			row.SetProvider(file.Provider)
		}
		if h.Mode {
			row.SetMode(file.Mode)
		}
		if h.CopyMode {
			row.SetCopyMode(file.CopyMode)
		}
		if h.Status {
			row.SetStatus(file.Status)
		}
		if h.PayloadID {
			row.SetPayloadId(file.PayloadID)
		}
		if h.ContainerID {
			row.SetContainerId(file.ContainerID)
		}
		if h.ProviderFileID {
			row.SetProviderFileId(file.ProviderFileID)
		}
		if h.Filename {
			row.SetFilename(file.Filename)
		}
		if h.MimeType {
			row.SetMimeType(file.MimeType)
		}
		if h.SizeBytes {
			row.SetSizeBytes(file.SizeBytes)
		}
		if h.Checksum {
			row.SetChecksum(file.Checksum)
		}
		if h.ErrorMessage {
			row.SetErrorMessage(file.ErrorMessage)
		}
		if h.ExpiresAt {
			row.SetExpiresAt(file.ExpiresAt)
		}
		if h.CreatedAt {
			row.SetCreatedAt(file.CreatedAt)
		}
		if h.UpdatedAt {
			row.SetUpdatedAt(file.UpdatedAt)
		}
	}
	initial := nativePresence(row)
	output, err := (&store.GeneratedFileStore{Invoker: s.native}).PatchTrustedResult(ctx, row)
	if err != nil {
		return err
	}
	return applyNativeMutation(file, output.Data, initial)
}

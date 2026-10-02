package conversation

import (
	"context"
	"fmt"
	"strings"

	convcli "github.com/viant/agently-core/app/store/conversation"
	write "github.com/viant/agently-core/internal/datly/payload/write"
	store "github.com/viant/agently-core/internal/store/conversation"
)

func (s *Service) getPayloadNative(ctx context.Context, id string) (*convcli.Payload, error) {
	row, err := (&store.PayloadStore{Invoker: s.native}).Get(ctx, id)
	if err != nil || row == nil {
		return nil, err
	}
	return &convcli.Payload{
		Id: row.Id, TenantID: row.TenantId, Kind: row.Kind,
		Subtype: row.Subtype, MimeType: row.MimeType, SizeBytes: row.SizeBytes,
		Digest: row.Digest, Storage: row.Storage, InlineBody: row.InlineBody,
		URI: row.Uri, Compression: row.Compression,
		EncryptionKMSKeyID:     row.EncryptionKmsKeyId,
		RedactionPolicyVersion: row.RedactionPolicyVersion,
		Redacted:               row.Redacted, CreatedAt: row.CreatedAt, SchemaRef: row.SchemaRef,
	}, nil
}

func (s *Service) patchPayloadNative(ctx context.Context, payload *convcli.MutablePayload) error {
	if payload == nil {
		return fmt.Errorf("payload mutation is required")
	}
	row := &write.Payload{}
	row.SetId(strings.TrimSpace(payload.Id))
	if h := payload.Has; h != nil {
		if h.TenantID {
			row.SetTenantId(payload.TenantID)
		}
		if h.Kind {
			row.SetKind(payload.Kind)
		}
		if h.Subtype {
			row.SetSubtype(payload.Subtype)
		}
		if h.MimeType {
			row.SetMimeType(payload.MimeType)
		}
		if h.SizeBytes {
			row.SetSizeBytes(payload.SizeBytes)
		}
		if h.Digest {
			row.SetDigest(payload.Digest)
		}
		if h.Storage {
			row.SetStorage(payload.Storage)
		}
		if h.InlineBody {
			row.SetInlineBody(payload.InlineBody)
		}
		if h.URI {
			row.SetUri(payload.URI)
		}
		if h.Compression {
			row.SetCompression(payload.Compression)
		}
		if h.EncryptionKMSKeyID {
			row.SetEncryptionKmsKeyId(payload.EncryptionKMSKeyID)
		}
		if h.RedactionPolicyVersion {
			row.SetRedactionPolicyVersion(payload.RedactionPolicyVersion)
		}
		if h.Redacted {
			row.SetRedacted(payload.Redacted)
		}
		if h.CreatedAt {
			row.SetCreatedAt(payload.CreatedAt)
		}
		if h.SchemaRef {
			row.SetSchemaRef(payload.SchemaRef)
		}
	}
	initial := nativePresence(row)
	output, err := (&store.PayloadStore{Invoker: s.native}).PatchTrustedResult(ctx, row)
	if err != nil {
		return err
	}
	return applyNativeMutation(payload, output.Data, initial)
}

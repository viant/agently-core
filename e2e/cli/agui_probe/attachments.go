package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/sdk"
	agentsvc "github.com/viant/agently-core/service/agent"
	"net/url"
	"strings"
)

// Inline test inputs must enter the same supported authenticated file API as a
// user upload. Canonical AG-UI accepts actual resource references, not data bytes.
func prepareInlineAttachments(ctx context.Context, client *sdk.HTTPClient, input *agentsvc.QueryInput) (*agentsvc.QueryInput, error) {
	if input == nil {
		return nil, fmt.Errorf("query input missing")
	}
	hasInline := false
	for _, a := range input.Attachments {
		if a == nil {
			return nil, fmt.Errorf("nil attachment")
		}
		if len(a.Data) > 0 {
			if a.URI != "" || a.StagingFolder != "" {
				return nil, fmt.Errorf("ambiguous inline attachment")
			}
			hasInline = true
		}
	}
	if !hasInline {
		return input, nil
	}
	q := *input
	q.Attachments = make([]*binding.Attachment, 0, len(input.Attachments))
	if q.ConversationID == "" {
		conversation, err := client.CreateConversation(ctx, &sdk.CreateConversationInput{AgentID: q.AgentID, Title: q.ConversationTitle, ParentConversationID: q.ParentConversationID})
		if err != nil {
			return nil, err
		}
		if conversation == nil || conversation.Id == "" {
			return nil, fmt.Errorf("conversation creation missing identity")
		}
		q.ConversationID = conversation.Id
		q.ConversationTitle = ""
		q.ParentConversationID = ""
	}
	for _, original := range input.Attachments {
		a := *original
		if len(a.Data) > 0 {
			uploaded, err := client.UploadFile(ctx, &sdk.UploadFileInput{ConversationID: q.ConversationID, Name: a.Name, ContentType: a.Mime, Data: a.Data})
			if err != nil {
				return nil, err
			}
			if uploaded == nil || uploaded.Resource == nil || strings.TrimSpace(uploaded.Resource.URI) == "" || uploaded.ID == "" || uploaded.ID != uploaded.Resource.ID {
				return nil, fmt.Errorf("authenticated upload missing actual resource reference")
			}
			expected := sha256.Sum256(a.Data)
			if uploaded.Resource.SizeBytes != int64(len(a.Data)) || uploaded.Resource.SHA256 != hex.EncodeToString(expected[:]) || uploaded.Resource.MimeType != a.Mime {
				return nil, fmt.Errorf("actual uploaded resource differs from authored bytes/MIME")
			}
			a.URI = "/v1/files/" + url.PathEscape(uploaded.ID) + "?" + url.Values{"conversationId": {q.ConversationID}}.Encode()
			a.Data = nil
		}
		q.Attachments = append(q.Attachments, &a)
	}
	return &q, nil
}

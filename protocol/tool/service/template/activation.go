package template

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/protocol/binding"
	tpldef "github.com/viant/agently-core/protocol/template"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/shared/toolexec"
)

type boundDocumentsKey struct{}

type boundDocuments struct {
	conversationID string
	turnID         string
	items          []*binding.Document
}

// WithBoundDocuments passes the current model binding to template activation.
func WithBoundDocuments(ctx context.Context, documents []*binding.Document) context.Context {
	turn, _ := runtimerequestctx.TurnMetaFromContext(ctx)
	return context.WithValue(ctx, boundDocumentsKey{}, boundDocuments{
		conversationID: turn.ConversationID,
		turnID:         turn.TurnID,
		items:          documents,
	})
}

// DocumentURI identifies a template system document by its admitted resource.
func DocumentURI(tpl *tpldef.Template) string {
	if uri := strings.TrimSpace(tpl.ResourceURI); uri != "" {
		return uri
	}
	return "template://" + strings.TrimSpace(tpl.ID)
}

func (s *Service) activateDocument(ctx context.Context, turn runtimerequestctx.TurnMeta, tpl *tpldef.Template) (GetOutput, error) {
	var receipt GetOutput
	if s.conv == nil {
		return receipt, fmt.Errorf("template activation requires conversation storage")
	}
	uri := DocumentURI(tpl)
	receipt = GetOutput{Name: strings.TrimSpace(tpl.Name), URI: uri, IncludedDocument: true}
	bound, _ := ctx.Value(boundDocumentsKey{}).(boundDocuments)
	var documents []*binding.Document
	if bound.conversationID == turn.ConversationID && bound.turnID == turn.TurnID {
		documents = bound.items
	}
	content := RenderTemplateDocument(tpl)
	if doc := matchingBoundDocument(documents, uri, content); doc != nil {
		receipt.MessageID = doc.Metadata["messageId"]
		return receipt, nil
	}
	if len(tpl.Schema) > 0 {
		raw, err := json.Marshal(tpl.Schema)
		if err != nil {
			return GetOutput{}, fmt.Errorf("render template output schema: %w", err)
		}
		if content == "" {
			content = "Active template: " + uri
		}
		content += "\n\nOutput schema:\n" + string(raw)
		if doc := matchingBoundDocument(documents, uri, content); doc != nil {
			receipt.MessageID = doc.Metadata["messageId"]
			return receipt, nil
		}
	}
	if content == "" {
		return GetOutput{}, fmt.Errorf("template has no document to activate")
	}
	// Upsert the same message for repeated or concurrent activation of this
	// template in this turn. Different resource URIs have separate messages.
	messageID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("template\x00"+turn.ConversationID+"\x00"+turn.TurnID+"\x00"+uri)).String()
	message, err := apiconv.AddMessage(ctx, s.conv, &turn,
		apiconv.WithId(messageID),
		apiconv.WithRole("system"),
		apiconv.WithCreatedByUserID("template"),
		apiconv.WithMode(toolexec.SystemDocumentMode),
		apiconv.WithContent(content),
		apiconv.WithTags(toolexec.SystemDocumentTag),
		apiconv.WithContextSummary(uri),
		apiconv.WithCreatedAt(time.Now()),
	)
	if err != nil {
		return GetOutput{}, err
	}
	receipt.MessageID = message.Id
	return receipt, nil
}

func matchingBoundDocument(documents []*binding.Document, uri, content string) *binding.Document {
	if content == "" {
		return nil
	}
	for _, doc := range documents {
		if doc != nil && doc.SourceURI == uri && doc.PageContent == content {
			return doc
		}
	}
	return nil
}

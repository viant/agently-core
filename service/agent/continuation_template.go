package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/service/core"
)

// continuationTemplatePublication lives for one plan loop, including its tool iterations.
// It only suppresses publication when a successful provider response proves that
// the current anchor already contains the same trusted document snapshot.
type continuationTemplatePublication struct {
	digest     string
	responseID string
}

func (p *continuationTemplatePublication) prepare(input *core.GenerateInput) (string, func()) {
	if input == nil || input.Binding == nil {
		return "", func() {}
	}
	b := input.Binding
	var documents []string
	var trusted []*binding.Document
	for _, doc := range b.SystemDocuments.Items {
		if doc != nil && doc.RefreshOnContinuation {
			documents = append(documents, doc.SourceURI, doc.PageContent)
			trusted = append(trusted, doc)
		}
	}
	if len(documents) == 0 {
		return "", func() {}
	}
	data, _ := json.Marshal(documents)
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if p.responseID != "" && p.digest == digest && b.History.LastResponse != nil && strings.TrimSpace(b.History.LastResponse.ID) == p.responseID {
		for _, doc := range trusted {
			doc.RefreshOnContinuation = false
		}
		var suppressedMessages []*llm.Message
		for index := range input.Message {
			message := &input.Message[index]
			if !message.RefreshOnContinuation || message.Role != llm.RoleSystem || len(message.ToolCalls) != 0 || message.ToolCallId != "" {
				continue
			}
			for _, doc := range trusted {
				if message.Content == doc.PageContent {
					message.RefreshOnContinuation = false
					suppressedMessages = append(suppressedMessages, message)
					break
				}
			}
		}
		return digest, func() {
			for _, message := range suppressedMessages {
				message.RefreshOnContinuation = true
			}
			for _, doc := range trusted {
				doc.RefreshOnContinuation = true
			}
		}
	}
	return digest, func() {}
}

func (p *continuationTemplatePublication) accept(digest string, response *llm.GenerateResponse) {
	if digest == "" || response == nil || strings.TrimSpace(response.ResponseID) == "" {
		return
	}
	p.digest = digest
	p.responseID = strings.TrimSpace(response.ResponseID)
}

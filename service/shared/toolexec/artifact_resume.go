package toolexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/tool/dispatchpayload"
	asynccfg "github.com/viant/agently-core/protocol/async"
	"github.com/viant/agently-core/protocol/tool"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

func schemaHasBinaryInput(shape map[string]interface{}) bool {
	format, _ := shape["format"].(string)
	encoding, _ := shape["contentEncoding"].(string)
	if format == "byte" || format == "binary" || encoding != "" {
		return true
	}
	properties, _ := shape["properties"].(map[string]interface{})
	for _, value := range properties {
		if child, ok := value.(map[string]interface{}); ok && schemaHasBinaryInput(child) {
			return true
		}
	}
	items, _ := shape["items"].(map[string]interface{})
	return items != nil && schemaHasBinaryInput(items)
}

// Reject persisted artifact resumptions whose original isolated server session
// cannot be recovered. This never guesses owner, tool identity or operation IDs.
func checkPersistedArtifactResume(ctx context.Context, reg tool.Registry, conv apiconv.Client, cfg *asynccfg.Config, opID string) error {
	definition, ok := reg.GetDefinition(cfg.Run.Tool)
	if getter, enabled := reg.(tool.ContextDefinitionGetter); enabled {
		definition, ok = getter.GetDefinitionWithContext(ctx, cfg.Run.Tool)
	}
	if !ok || definition == nil || (!schemaHasBinaryInput(definition.Parameters) && !dispatchpayload.HasArtifactReference(cfg.Run.ExtraArgs)) {
		return nil
	}
	if conv == nil || strings.TrimSpace(authctx.EffectiveUserID(ctx)) == "" {
		return fmt.Errorf("artifact-capable async resumption requires an owned conversation")
	}
	cid := requestctx.ConversationIDFromContext(ctx)
	row, err := conv.GetConversation(ctx, cid)
	if err != nil || row == nil {
		return fmt.Errorf("artifact-capable async resumption origin unavailable")
	}
	if row.CreatedByUserId == nil || *row.CreatedByUserId != authctx.EffectiveUserID(ctx) {
		return fmt.Errorf("artifact async operation owner mismatch")
	}
	row, err = conv.GetConversation(ctx, cid, apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true))
	if err != nil || row == nil {
		return fmt.Errorf("artifact async operation history unavailable")
	}
	matches, artifactMatches, artifactOrigins := 0, 0, 0
	for _, turn := range row.GetTranscript() {
		if turn == nil {
			continue
		}
		for _, message := range turn.GetMessages() {
			if message == nil {
				continue
			}
			name := ""
			if message.MessageToolCall != nil {
				name = message.MessageToolCall.ToolName
			} else {
				for _, entry := range message.ToolMessage {
					if entry != nil && entry.ToolCall != nil {
						name = entry.ToolCall.ToolName
						break
					}
				}
			}
			if !sameToolName(name, cfg.Run.Tool) {
				continue
			}
			args := message.ToolCallArguments()
			isArtifact := dispatchpayload.HasArtifactReference(args)
			if isArtifact {
				artifactOrigins++
			}
			var result map[string]interface{}
			if json.Unmarshal([]byte(message.GetContent()), &result) != nil {
				continue
			}
			value, found := asynccfgLookup(result, cfg.Run.OperationIDPath)
			if !found || fmt.Sprint(value) != opID {
				continue
			}
			matches++
			if isArtifact {
				artifactMatches++
			}
		}
	}
	if matches > 1 {
		return fmt.Errorf("artifact async operation linkage ambiguous")
	}
	if artifactMatches > 0 {
		return fmt.Errorf("artifact async resumption requires a new reviewed request; original server session unavailable")
	}
	if matches == 0 && (artifactOrigins > 0 || dispatchpayload.HasArtifactReference(cfg.Run.ExtraArgs)) {
		return fmt.Errorf("artifact async operation linkage unavailable")
	}
	return nil
}

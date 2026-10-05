package sdk

import (
	"github.com/viant/agently-core/runtime/aguistate"
)

// Recovery reads native authoring history. Apply the same public presentation
// boundary as streaming before restoring standard messages to any client.
func aguiSanitizeRecoveredMessages(writer *aguiJournalWriter, native *aguiRecoveredNative) *aguiRecoveredNative {
	if native == nil {
		return nil
	}
	copied := *native
	copied.Messages = make([]aguistate.Object, 0, len(native.Messages))
	for _, message := range native.Messages {
		if message["role"] != "assistant" {
			copied.Messages = append(copied.Messages, message)
			continue
		}
		content, ok := message["content"].(string)
		if !ok {
			copied.Messages = append(copied.Messages, message)
			continue
		}
		plain, rich := plainAGUIContent(content, true)
		if !rich {
			copied.Messages = append(copied.Messages, message)
			continue
		}
		projected := aguistate.Object{}
		for key, value := range message {
			projected[key] = value
		}
		projected["content"] = plain
		copied.Messages = append(copied.Messages, projected)
		rendered := normalizeRenderedContent(content, true)
		if rendered != nil {
			applyInlineReportWorkspaceCatalog(writer.ctx, rendered, writer.client)
			id, _ := message["id"].(string)
			activity := aguistate.Object{"id": id + "/activity", "role": "activity", "activityType": "agently.rendered-content", "content": map[string]any{"version": "1", "renderedContent": rendered}}
			if owner, ok := message["subagentRunId"]; ok {
				activity["subagentRunId"] = owner
			}
			copied.Messages = append(copied.Messages, activity)
		}
	}
	return &copied
}

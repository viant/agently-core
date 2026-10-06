package sdk

import (
	"context"
	"encoding/json"
	"fmt"

	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/requestctx"
)

// Non-root apps must be attributed to an immutable framework invocation that
// was registered before their actual native response. Client metadata and
// mutable conversation parent pointers cannot create this authority.
func validateMCPAppJournalAncestry(ctx context.Context, store aguistore.Store, run *aguistore.Run, raw json.RawMessage, sequence int64) error {
	var event struct {
		Type     string `json:"type"`
		Activity string `json:"activityType"`
		Owner    string `json:"subagentRunId"`
		Content  struct {
			Binding AGUIMCPAppBinding `json:"_agentlyApp"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &event) != nil || event.Type != "ACTIVITY_SNAPSHOT" || event.Activity != "mcp-apps" {
		return nil
	}
	app := event.Content.Binding
	if app.ThreadID == run.ConversationID && (app.NativeTurnID == "" || app.NativeTurnID == run.TurnID) {
		return nil
	}
	if app.NativeTurnID == "" || app.InvocationID == "" || event.Owner != app.InvocationID {
		return fmt.Errorf("MCP child app attribution differs from native invocation")
	}
	var prefix []json.RawMessage
	var invocation requestctx.Invocation
	var after int64
	through := sequence - 1
	if run.LastSequence < through {
		through = run.LastSequence
	}
	for after < through {
		page, err := store.Replay(ctx, run.Principal, run.ThreadID, run.RunID, after, 256)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return fmt.Errorf("MCP app invocation journal incomplete")
		}
		for _, entry := range page {
			if entry.Sequence > through {
				break
			}
			if entry.Sequence != after+1 {
				return fmt.Errorf("MCP app invocation journal gap")
			}
			prefix = append(prefix, entry.Event)
			after = entry.Sequence
			var checkpoint struct {
				Type  string `json:"type"`
				Name  string `json:"name"`
				Value struct {
					Version    string                `json:"version"`
					Phase      string                `json:"phase"`
					Invocation requestctx.Invocation `json:"invocation"`
				} `json:"value"`
			}
			if json.Unmarshal(entry.Event, &checkpoint) == nil && checkpoint.Type == "CUSTOM" && checkpoint.Name == "agently.invocation" && checkpoint.Value.Version == "1" && checkpoint.Value.Phase == "registered" && checkpoint.Value.Invocation.ID == app.InvocationID {
				invocation = checkpoint.Value.Invocation
			}
		}
	}
	if len(prefix) == 0 {
		return fmt.Errorf("MCP app native identity checkpoint missing")
	}
	var frame struct {
		Type     string `json:"type"`
		Metadata struct {
			Agently struct {
				Version string `json:"identityVersion"`
				Turn    string `json:"nativeTurnId"`
			} `json:"agently"`
		} `json:"metadata"`
	}
	if json.Unmarshal(prefix[0], &frame) != nil || frame.Type != "RUN_STARTED" || frame.Metadata.Agently.Version != "1" || frame.Metadata.Agently.Turn != run.TurnID {
		return fmt.Errorf("MCP app native journal frame differs from root")
	}
	if invocation.ID != app.InvocationID || invocation.ConversationID != app.ThreadID || invocation.TurnID != app.NativeTurnID || invocation.Detached {
		return fmt.Errorf("MCP app original conversation differs from invocation checkpoint")
	}
	// Reuse the translator's immutable ancestry/duplicate-ID/cycle validation.
	_, err := agui.RestoreTranslator(run.ThreadID, run.RunID, prefix)
	return err
}

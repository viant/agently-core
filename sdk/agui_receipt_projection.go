package sdk

import (
	"context"
	"fmt"

	"reflect"

	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/aguistate"
)

// Completion uses the original backend tool identity; approval answers do not
// create frontend tool calls or a synthetic assistant step.
func aguiProjectApprovalReceipt(ctx context.Context, runtime aguiRuntime, writer *aguiJournalWriter, translator *agui.Translator, interrupt agui.WireInterrupt) error {
	if interrupt.Reason != "approval" {
		return nil
	}
	if interrupt.ToolCallID == nil {
		return fmt.Errorf("approval has no original tool call identity")
	}
	inspector, ok := runtime.(aguiRecoveryRuntime)
	if !ok {
		return fmt.Errorf("approval receipt requires native execution inspection")
	}
	native, err := inspector.aguiInspectRun(ctx, writer.run)
	if err != nil {
		return err
	}
	if native == nil {
		return fmt.Errorf("approval native turn disappeared")
	}
	native = aguiSanitizeRecoveredMessages(writer, native)
	for _, message := range native.Messages {
		if message["role"] != "tool" || message["toolCallId"] != *interrupt.ToolCallID {
			continue
		}
		event := map[string]any{"type": "TOOL_CALL_RESULT", "messageId": message["id"], "toolCallId": message["toolCallId"], "content": message["content"], "role": "tool"}
		if metadata, ok := message["metadata"]; ok {
			event["metadata"] = metadata
		}
		already, lookupErr := aguiReceiptResultRecorded(ctx, writer, event)
		if lookupErr != nil {
			return lookupErr
		}
		if already {
			return nil
		}
		// An approved edit may have updated the original request arguments.
		// Reconcile those authoritative descriptors before publishing RESULT.
		var snapshot []aguistate.Object
		for _, candidate := range aguiMergeRecoveryMessages(writer.projection.Messages, native.Messages) {
			if candidate["role"] == "tool" && candidate["toolCallId"] == *interrupt.ToolCallID {
				continue
			}
			snapshot = append(snapshot, candidate)
		}
		snapshotEvent, decodeErr := agui.DecodeEvent(rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": snapshot}))
		if decodeErr != nil {
			return decodeErr
		}
		wire, decodeErr := agui.DecodeEvent(rawAGUI(event))
		if decodeErr != nil {
			return decodeErr
		}
		events, decodeErr := translator.ReconcileApprovalReceipt(writer.run.TurnID, *interrupt.ToolCallID, snapshotEvent, wire)
		if decodeErr != nil {
			return decodeErr
		}
		return writer.write(encodeAGUIEvents(events), nil)
	}
	return fmt.Errorf("completed approval has no authoritative original tool result")
}

// Only an actual RESULT in this run proves this journal step committed. A tool
// message inherited from an older snapshot does not suppress the current result.
func aguiReceiptResultRecorded(ctx context.Context, writer *aguiJournalWriter, event map[string]any) (bool, error) {
	after := int64(0)
	for {
		entries, err := writer.store.Replay(ctx, writer.run.Principal, writer.run.ThreadID, writer.run.RunID, after, 256)
		if err != nil {
			return false, err
		}
		for _, entry := range entries {
			after = entry.Sequence
			var recorded map[string]any
			if err = decodeAGUIValue(entry.Event, &recorded); err != nil {
				return false, err
			}
			if recorded["type"] != "TOOL_CALL_RESULT" || recorded["toolCallId"] != event["toolCallId"] {
				continue
			}
			var expected map[string]any
			if err = decodeAGUIValue(rawAGUI(event), &expected); err != nil {
				return false, err
			}
			if recorded["messageId"] != expected["messageId"] || !reflect.DeepEqual(recorded["content"], expected["content"]) {
				return false, fmt.Errorf("journal tool result conflicts with its native receipt")
			}
			return true, nil
		}
		if len(entries) < 256 {
			return false, nil
		}
	}
}

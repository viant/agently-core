package agui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	wire "github.com/viant/agently-core/protocol/agui"
)

const ContinuationControlRestoreFailedCode = "CONTINUATION_CONTROL_RESTORE_FAILED"
const ContinuationControlRestoreFailedMessage = "Continuation controls could not be restored before runtime."

// FailedContinuationRestoration accepts only an initialization journal which
// stopped before snapshots, native continuation or any model/tool event.
// The legacy error also covered cycles; a caller must revalidate the complete
// lineage before recovery, so the message alone never authorizes continuation.
func FailedContinuationRestoration(run *Run, journal []json.RawMessage) bool {
	if run == nil || run.Status != StatusError || run.PriorRunID == "" || run.ParentRunID != run.PriorRunID || run.TurnID == "" || run.ClientMessageID != "" || len(journal) != 2 || run.LastSequence != 2 {
		return false
	}
	for _, event := range journal {
		if wire.ValidateEvent(event) != nil {
			return false
		}
	}
	var start struct{ Type, ThreadID, RunID string }
	var failed struct{ Type, Code, Message string }
	if json.Unmarshal(journal[0], &start) != nil || json.Unmarshal(journal[1], &failed) != nil || start.Type != "RUN_STARTED" || start.ThreadID != run.ThreadID || start.RunID != run.RunID || failed.Type != "RUN_ERROR" {
		return false
	}
	return failed.Code == ContinuationControlRestoreFailedCode && failed.Message == ContinuationControlRestoreFailedMessage || failed.Code == "EXECUTION_FAILED" && failed.Message == "continuation lineage is cyclic or exceeds its bound"
}

func FailedContinuationRecoveryID(run *Run) string {
	raw, _ := json.Marshal([]string{"agui.continuation-recovery.v1", run.Principal, run.ThreadID, run.RunID, run.InputHash})
	digest := sha256.Sum256(raw)
	return "continuation-recovery-" + hex.EncodeToString(digest[:])
}

// Recovery copies the server's already accepted input. It cannot replace an
// answer, tool, execution control or native identity; only protocol run linkage
// changes. The failed run and its original input/journal remain immutable.
func FailedContinuationRecoveryInput(run *Run) (json.RawMessage, error) {
	var input map[string]json.RawMessage
	if run == nil || json.Unmarshal(run.Input, &input) != nil || input == nil {
		return nil, fmt.Errorf("accepted continuation input unavailable")
	}
	var threadID, runID string
	if json.Unmarshal(input["threadId"], &threadID) != nil || json.Unmarshal(input["runId"], &runID) != nil || threadID != run.ThreadID || runID != run.RunID {
		return nil, fmt.Errorf("accepted continuation input identity mismatch")
	}
	var messages []struct {
		ID, Role, ToolCallID string
		Content              json.RawMessage
	}
	if json.Unmarshal(input["messages"], &messages) != nil {
		return nil, fmt.Errorf("accepted continuation messages unavailable")
	}
	results := map[string]bool{}
	for _, message := range messages {
		if message.Role != "tool" {
			continue
		}
		if message.ID == "" || message.ToolCallID == "" || len(message.Content) == 0 || results[message.ToolCallID] {
			return nil, fmt.Errorf("accepted continuation result identity invalid")
		}
		results[message.ToolCallID] = true
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("accepted continuation has no tool results")
	}
	input["runId"], _ = json.Marshal(FailedContinuationRecoveryID(run))
	input["parentRunId"], _ = json.Marshal(run.RunID)
	return json.Marshal(input)
}

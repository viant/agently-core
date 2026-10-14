package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	aguistore "github.com/viant/agently-core/app/store/agui"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/clienttool"
)

type aguiContinuationControlRestorationFailure struct{ cause error }

func (e *aguiContinuationControlRestorationFailure) Error() string {
	return aguistore.ContinuationControlRestoreFailedMessage
}
func (e *aguiContinuationControlRestorationFailure) Unwrap() error { return e.cause }

// An attachment may recover this one already accepted answer, never execute a
// frontend tool. A deterministic successor preserves the failed terminal audit.
func prepareFailedAGUIContinuationRecovery(ctx context.Context, client Client, runtime aguiRuntime, store aguistore.Store, failed *aguistore.Run) (*aguistore.Run, error) {
	if failed == nil || failed.Status != aguistore.StatusError || failed.PriorRunID == "" {
		return nil, nil
	}
	if iauth.EffectiveUserID(ctx) != failed.Principal {
		return nil, fmt.Errorf("failed continuation owner mismatch")
	}
	journal, err := aguiRecoveryJournal(ctx, store, failed)
	if err != nil {
		return nil, err
	}
	if !aguistore.FailedContinuationRestoration(failed, journal) {
		return nil, nil
	}
	body, err := aguistore.FailedContinuationRecoveryInput(failed)
	if err != nil {
		return nil, err
	}
	if failed.ResumedByRunID != "" {
		successor, err := store.GetRun(ctx, failed.Principal, failed.ThreadID, failed.ResumedByRunID)
		if err != nil {
			return nil, err
		}
		if successor == nil {
			return nil, fmt.Errorf("failed continuation successor unavailable")
		}
		want, _ := canonicalJSONValue(body)
		actual, e := canonicalJSONValue(successor.Input)
		if e != nil || successor == nil || successor.RunID != aguistore.FailedContinuationRecoveryID(failed) || successor.Principal != failed.Principal || successor.ThreadID != failed.ThreadID || successor.ConversationID != failed.ConversationID || successor.TurnID != failed.TurnID || successor.PriorRunID != failed.PriorRunID || successor.ParentRunID != failed.RunID || !bytes.Equal(want, actual) {
			return nil, fmt.Errorf("failed continuation successor identity mismatch")
		}
		return successor, nil
	}
	prior, err := store.GetRun(ctx, failed.Principal, failed.ThreadID, failed.PriorRunID)
	if err != nil {
		return nil, err
	}
	if prior == nil || prior.Status != aguistore.StatusInterrupted || prior.ResumedByRunID != failed.RunID || prior.Principal != failed.Principal || prior.ThreadID != failed.ThreadID || prior.ConversationID != failed.ConversationID || prior.TurnID != failed.TurnID {
		return nil, fmt.Errorf("failed continuation consumed parent mismatch")
	}
	var pending aguiPending
	var accepted agui.RunAgentInput
	if json.Unmarshal(prior.Pending, &pending) != nil || json.Unmarshal(failed.Input, &accepted) != nil || len(pending.ClientTools) == 0 || len(pending.Interrupts) != 0 || len(pending.Dependencies) != 0 || len(accepted.Resume) != 0 {
		return nil, fmt.Errorf("failed continuation requires accepted client tool results")
	}
	for _, call := range pending.ClientTools {
		matches := 0
		for _, message := range accepted.Messages {
			if message.Role == "tool" && message.ToolCallID == aguiPendingCallID(call) {
				matches++
			}
		}
		if matches != 1 {
			return nil, fmt.Errorf("failed continuation accepted result mismatch")
		}
	}
	var properties agui.ForwardedProps
	if len(accepted.ForwardedProps) > 0 && json.Unmarshal(accepted.ForwardedProps, &properties) != nil {
		return nil, fmt.Errorf("failed continuation controls unavailable")
	}
	query, err := queryForAGUI(&accepted, failed.Principal, failed.TurnID, properties.Agently, prior)
	if err != nil {
		return nil, err
	}
	if err = restoreAGUIContinuationControls(ctx, store, failed, query); err != nil {
		return nil, err
	}
	inspector, ok := runtime.(aguiRecoveryRuntime)
	if !ok {
		return nil, fmt.Errorf("failed continuation native inspection unavailable")
	}
	native, err := inspector.aguiInspectRun(ctx, failed)
	if err != nil {
		return nil, err
	}
	if native == nil || !strings.EqualFold(native.Status, "waiting_for_user") && !strings.EqualFold(native.Status, "blocked") || !sameFailedContinuationCalls(pending.ClientTools, native.Pending.ClientTools, failed) || len(native.Pending.Interrupts) != 0 || len(native.Pending.Dependencies) != 0 {
		return nil, fmt.Errorf("failed continuation native boundary changed")
	}
	successor, _, err := store.Admit(ctx, aguistore.Admission{Principal: failed.Principal, ThreadID: failed.ThreadID, RunID: aguistore.FailedContinuationRecoveryID(failed), ParentRunID: failed.RunID, PriorRunID: prior.RunID, ExpectedPriorRevision: prior.Revision, ExpectedFailedRevision: failed.Revision, TurnID: failed.TurnID, Input: body})
	return successor, err
}

func sameFailedContinuationCalls(expected, actual []clienttool.PendingCall, run *aguistore.Run) bool {
	if len(expected) != len(actual) {
		return false
	}
	seen := map[string]bool{}
	for _, saved := range expected {
		if saved.ConversationID != run.ConversationID || saved.TurnID != run.TurnID {
			return false
		}
		found := false
		for _, current := range actual {
			if current.ID != saved.ID || seen[current.ID] {
				continue
			}
			before, e1 := canonicalJSONValue(rawAGUI(saved.Arguments))
			after, e2 := canonicalJSONValue(rawAGUI(current.Arguments))
			if current.Name != saved.Name || current.ToolMessageID != saved.ToolMessageID || current.AssistantMessageID != saved.AssistantMessageID || current.ConversationID != run.ConversationID || current.TurnID != run.TurnID || e1 != nil || e2 != nil || !bytes.Equal(before, after) {
				return false
			}
			seen[current.ID], found = true, true
			break
		}
		if !found {
			return false
		}
	}
	return true
}

// Existing CAS append and observer Claim own all publication/execution. No
// failed journal is reopened and no accepted frontend tool is invoked here.
func runFailedAGUIContinuationRecovery(ctx context.Context, client Client, runtime aguiRuntime, store aguistore.Store, successor *aguistore.Run) (returnErr error) {
	if successor == nil {
		return fmt.Errorf("accepted continuation successor unavailable")
	}
	expected := *successor
	defer func() {
		if !errors.Is(returnErr, aguistore.ErrConflict) && !errors.Is(returnErr, aguistore.ErrInvalidTransition) {
			return
		}
		// Another observer can start/finish between our read and append/Claim.
		// Follow only this exact admitted successor; never retry execution or
		// turn an unrelated lifecycle failure into apparent success.
		current, err := store.GetRun(ctx, expected.Principal, expected.ThreadID, expected.RunID)
		if err != nil || current == nil || current.RunID != expected.RunID || current.Principal != expected.Principal || current.ThreadID != expected.ThreadID || current.ConversationID != expected.ConversationID || current.TurnID != expected.TurnID || current.ParentRunID != expected.ParentRunID || current.PriorRunID != expected.PriorRunID || current.InputHash != expected.InputHash || !bytes.Equal(current.Input, expected.Input) || current.Revision < expected.Revision || current.LastSequence == 0 {
			return
		}
		failed, err := store.GetRun(ctx, expected.Principal, expected.ThreadID, expected.ParentRunID)
		if err != nil || failed == nil || failed.Status != aguistore.StatusError || failed.ResumedByRunID != current.RunID || failed.PriorRunID != current.PriorRunID || failed.TurnID != current.TurnID || failed.ConversationID != current.ConversationID || failed.Principal != current.Principal || failed.ThreadID != current.ThreadID {
			return
		}
		switch current.Status {
		case aguistore.StatusRunning, aguistore.StatusFinished, aguistore.StatusInterrupted:
			returnErr = nil // Existing attachment observes the owner's result.
		}
	}()
	if successor.Status == aguistore.StatusAdmitted {
		translator := agui.NewTranslator(successor.ThreadID, successor.RunID)
		if err := translator.SetNativeIdentity(successor.TurnID); err != nil {
			return err
		}
		started := encodeAGUIEvents(translator.Start())
		var opening map[string]json.RawMessage
		_ = json.Unmarshal(started[0], &opening)
		opening["parentRunId"] = rawAGUI(successor.ParentRunID)
		started[0] = rawAGUI(opening)
		var err error
		successor, err = store.Append(ctx, successor.Principal, successor.ThreadID, successor.RunID, successor.Revision, started, nil)
		if err != nil {
			return err
		}
	}
	return recoverAGUIDurable(ctx, client, runtime, store, successor)
}

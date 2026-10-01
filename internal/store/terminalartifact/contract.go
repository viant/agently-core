package terminalartifact

import (
	"context"
	"fmt"
	dexec "github.com/viant/datly/exec"
	"strings"
	"time"
)

type Kind string

const (
	ModelCall Kind = "model_call"
	ToolCall  Kind = "tool_call"
	Message   Kind = "message"
)

type Linkage string

const (
	DirectTurn  Linkage = "direct_turn"
	MessageTurn Linkage = "message_turn"
	Run         Linkage = "run"
	LegacyRun   Linkage = "legacy_run"
)

type Candidate struct {
	Kind           Kind
	ID             string
	ConversationID string
	TurnID         string
	Linkage        Linkage
	ExpectedLink   string
	ExpectedRun    string
	TerminalStatus string
	Reason         string
}
type Disposition uint8

const (
	Unresolved Disposition = iota
	Repaired
	AlreadyResolved
	NoLongerEligible
)

func validate(candidate Candidate) error {
	if strings.TrimSpace(candidate.ID) == "" || strings.TrimSpace(candidate.TurnID) == "" || strings.TrimSpace(candidate.ConversationID) == "" {
		return fmt.Errorf("terminal artifact candidate identity is incomplete")
	}
	if !terminalTurn(candidate.TerminalStatus) {
		return fmt.Errorf("terminal artifact candidate status is invalid")
	}
	switch candidate.Kind {
	case ModelCall, ToolCall:
	case Message:
		if candidate.Linkage != DirectTurn {
			return fmt.Errorf("terminal artifact candidate kind/linkage is invalid")
		}
	default:
		return fmt.Errorf("terminal artifact candidate kind is invalid")
	}
	switch candidate.Linkage {
	case DirectTurn:
		if candidate.ExpectedLink == "" || candidate.ExpectedLink != candidate.TurnID {
			return fmt.Errorf("terminal artifact candidate direct linkage is invalid")
		}
	case MessageTurn:
		if candidate.Kind == Message || candidate.ExpectedLink == "" || candidate.ExpectedLink != candidate.TurnID {
			return fmt.Errorf("terminal artifact candidate message linkage is invalid")
		}
	case Run:
		if candidate.Kind == Message || candidate.ExpectedRun == "" {
			return fmt.Errorf("terminal artifact candidate run linkage is invalid")
		}
	case LegacyRun:
		if candidate.Kind == Message || candidate.ExpectedRun == "" || candidate.ExpectedRun != candidate.TurnID {
			return fmt.Errorf("terminal artifact candidate legacy linkage is invalid")
		}
	default:
		return fmt.Errorf("terminal artifact candidate linkage is invalid")
	}
	return nil
}
func terminalTurn(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "succeeded", "canceled":
		return true
	}
	return false
}
func terminalArtifact(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "canceled", "cancelled", "succeeded":
		return true
	}
	return false
}
func normalized(status string) string { return strings.ToLower(strings.TrimSpace(status)) }
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

type Input struct {
	Candidates  []Candidate `parameter:"Candidates,kind=body,in=candidates"`
	CompletedAt time.Time   `parameter:"CompletedAt,kind=body,in=completedAt"`
}
type Output struct {
	Dispositions []Disposition `json:"dispositions"`
}

// cleanup classifies from freshly locked generated rows before each write. The
// same managed invocation retains locks through completion and batch rollback.

func sqlTerminalArtifact(status string) bool {
	switch status {
	case "completed", "failed", "canceled", "cancelled", "succeeded":
		return true
	}
	return false
}
func sqlTerminalTurn(status string) bool {
	switch status {
	case "failed", "succeeded", "canceled":
		return true
	}
	return false
}
func capturedLinkMatches(ctx context.Context, invoker dexec.ComponentInvoker, candidate Candidate, artifact artifactState, turn turnState) (bool, error) {
	switch candidate.Linkage {
	case DirectTurn:
		return artifact.turnID == candidate.ExpectedLink, nil
	case MessageTurn:
		message, found, err := readMessage(ctx, invoker, candidate.ID)
		return found && message.turnID == candidate.ExpectedLink, err
	case Run:
		return artifact.runID == candidate.ExpectedRun && turn.runID == candidate.ExpectedRun, nil
	case LegacyRun:
		return artifact.runID == candidate.ExpectedRun, nil
	}
	return false, nil
}
func classify(ctx context.Context, invoker dexec.ComponentInvoker, candidate Candidate) (Disposition, error) {
	current, found, err := readArtifact(ctx, invoker, candidate)
	if err != nil {
		return Unresolved, err
	}
	if !found || terminalArtifact(current.status) {
		return AlreadyResolved, nil
	}
	turn, found, err := readTurn(ctx, invoker, candidate.TurnID)
	if err != nil {
		return Unresolved, err
	}
	if !found || !terminalTurn(turn.status) || normalized(turn.status) != normalized(candidate.TerminalStatus) || turn.conversationID != candidate.ConversationID {
		return NoLongerEligible, nil
	}
	matched, err := capturedLinkMatches(ctx, invoker, candidate, current, turn)
	if err != nil {
		return Unresolved, err
	}
	if !matched {
		return NoLongerEligible, nil
	}
	return Unresolved, nil
}
func cleanup(ctx context.Context, deps dependencies, input *Input, output *Output) error {
	output.Dispositions = make([]Disposition, len(input.Candidates))
	for i, candidate := range input.Candidates {
		if err := validate(candidate); err != nil {
			return err
		}
		current, found, err := readArtifact(ctx, deps.Invoker, candidate)
		if err != nil {
			return err
		}
		eligible := found && !sqlTerminalArtifact(current.cleanupStatus)
		if eligible {
			turn, turnFound, err := readTurn(ctx, deps.Invoker, candidate.TurnID)
			if err != nil {
				return err
			}
			eligible = turnFound && sqlTerminalTurn(turn.cleanupStatus) && turn.cleanupStatus == normalized(candidate.TerminalStatus) && turn.conversationID == candidate.ConversationID
			if eligible {
				eligible, err = capturedLinkMatches(ctx, deps.Invoker, candidate, current, turn)
				if err != nil {
					return err
				}
			}
		}
		if !eligible {
			disposition, err := classify(ctx, deps.Invoker, candidate)
			if err != nil {
				return err
			}
			output.Dispositions[i] = disposition
			continue
		}
		before := len(deps.Reporter.MutationReport().Results)
		if err := repair(ctx, deps.Invoker, candidate, input.CompletedAt.UTC()); err != nil {
			return err
		}
		report := deps.Reporter.MutationReport()
		if len(report.Results) != before+1 {
			return fmt.Errorf("terminal artifact repair produced %d mutation results", len(report.Results)-before)
		}
		result := report.Results[before]
		if result.Error != nil {
			return result.Error
		}
		if result.Table != string(candidate.Kind) || result.Operation != "update" || result.Records != 1 || result.Affected < 0 || result.Affected > 1 {
			return fmt.Errorf("terminal artifact update affected an unexpected number of rows")
		}
		if result.Affected == 1 {
			output.Dispositions[i] = Repaired
			continue
		}
		disposition, err := classify(ctx, deps.Invoker, candidate)
		if err != nil {
			return err
		}
		output.Dispositions[i] = disposition
	}
	return nil
}

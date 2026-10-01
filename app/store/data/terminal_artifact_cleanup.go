package data

import (
	"context"
	"time"

	"github.com/viant/agently-core/internal/sqlitewrite"
	"github.com/viant/agently-core/internal/store/terminalartifact"
)

// TerminalArtifactCleanupStore is an optional, narrow startup-maintenance
// capability. It deliberately is not part of Service so callers that do not
// use the Datly store do not need to expose startup maintenance methods.
type TerminalArtifactCleanupStore interface {
	SnapshotTerminalArtifactCandidates(ctx context.Context, terminalSince time.Time, terminalTurnLimit int) ([]TerminalArtifactCandidate, error)
	CleanupTerminalArtifactCandidates(ctx context.Context, candidates []TerminalArtifactCandidate, completedAt time.Time) ([]TerminalArtifactDisposition, error)
}

var _ TerminalArtifactCleanupStore = (*datlyService)(nil)

type TerminalArtifactKind string

const (
	TerminalArtifactModelCall TerminalArtifactKind = "model_call"
	TerminalArtifactToolCall  TerminalArtifactKind = "tool_call"
	TerminalArtifactMessage   TerminalArtifactKind = "message"
)

type TerminalArtifactLinkage string

const (
	TerminalArtifactDirectTurn  TerminalArtifactLinkage = "direct_turn"
	TerminalArtifactMessageTurn TerminalArtifactLinkage = "message_turn"
	TerminalArtifactRun         TerminalArtifactLinkage = "run"
	TerminalArtifactLegacyRun   TerminalArtifactLinkage = "legacy_run"
)

// TerminalArtifactCandidate contains only the identity and optimistic guard
// values captured at startup. Message content and artifact payloads are never
// retained in memory.
type TerminalArtifactCandidate struct {
	Kind           TerminalArtifactKind
	ID             string
	ConversationID string
	TurnID         string
	Linkage        TerminalArtifactLinkage
	ExpectedLink   string
	ExpectedRun    string
	TerminalStatus string
	Reason         string
}

type TerminalArtifactDisposition uint8

const (
	TerminalArtifactUnresolved TerminalArtifactDisposition = iota
	TerminalArtifactRepaired
	TerminalArtifactAlreadyResolved
	TerminalArtifactNoLongerEligible
)

func (s *datlyService) SnapshotTerminalArtifactCandidates(ctx context.Context, terminalSince time.Time, terminalTurnLimit int) ([]TerminalArtifactCandidate, error) {
	rows, err := (&terminalartifact.Store{Invoker: s.native}).Snapshot(ctx, terminalSince, terminalTurnLimit)
	if err != nil {
		return nil, err
	}
	result := make([]TerminalArtifactCandidate, 0, len(rows))
	for _, row := range rows {
		result = append(result, TerminalArtifactCandidate{Kind: TerminalArtifactKind(row.Kind), ID: row.ID, ConversationID: row.ConversationID, TurnID: row.TurnID, Linkage: TerminalArtifactLinkage(row.Linkage), ExpectedLink: row.ExpectedLink, ExpectedRun: row.ExpectedRun, TerminalStatus: row.TerminalStatus, Reason: row.Reason})
	}
	return result, nil
}

func (s *datlyService) CleanupTerminalArtifactCandidates(ctx context.Context, candidates []TerminalArtifactCandidate, completedAt time.Time) ([]TerminalArtifactDisposition, error) {
	if len(candidates) == 0 {
		return []TerminalArtifactDisposition{}, nil
	}
	return sqlitewrite.Do(ctx, s.writeGate, func() ([]TerminalArtifactDisposition, error) {
		input := make([]terminalartifact.Candidate, 0, len(candidates))
		for _, row := range candidates {
			input = append(input, terminalartifact.Candidate{Kind: terminalartifact.Kind(row.Kind), ID: row.ID, ConversationID: row.ConversationID, TurnID: row.TurnID, Linkage: terminalartifact.Linkage(row.Linkage), ExpectedLink: row.ExpectedLink, ExpectedRun: row.ExpectedRun, TerminalStatus: row.TerminalStatus, Reason: row.Reason})
		}
		rows, err := (&terminalartifact.Store{Invoker: s.native}).Cleanup(ctx, input, completedAt)
		if err != nil {
			return nil, err
		}
		result := make([]TerminalArtifactDisposition, len(rows))
		for i, row := range rows {
			result[i] = TerminalArtifactDisposition(row)
		}
		return result, nil
	})
}

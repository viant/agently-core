package reporting

import (
	"reflect"
	"strings"

	reportcontext "github.com/viant/agently-core/pkg/agently/reportcontext"
	reportrun "github.com/viant/agently-core/pkg/agently/reportrun"
)

// ValidateReportRunUpdate protects terminal snapshots even when a caller
// bypasses the report-run service and invokes a store directly.
func ValidateReportRunUpdate(current, next *reportrun.Record, expectedRevision int64) error {
	if current == nil || next == nil {
		return ErrNotFound
	}
	if current.Revision != expectedRevision {
		return ErrCASMismatch
	}
	if next.Revision != expectedRevision+1 {
		return ErrCASMismatch
	}
	if strings.TrimSpace(current.ReportRunID) != strings.TrimSpace(next.ReportRunID) ||
		strings.TrimSpace(current.OwnerID) != strings.TrimSpace(next.OwnerID) ||
		strings.TrimSpace(current.UIRunRequestID) != strings.TrimSpace(next.UIRunRequestID) {
		return ErrNotFound
	}
	if current.Status == reportrun.StatusCompleted {
		return ErrImmutable
	}
	return nil
}

// ValidateAdoptionMutation validates the only mutation permitted for a
// completed run: binding an unbound manual snapshot and advancing its exact
// conversation pointer in one store operation.
func ValidateAdoptionMutation(current, next *reportrun.Record, expectedRunRevision int64, record *reportcontext.Record, expectedContextRevision int64) error {
	if current == nil || next == nil || record == nil {
		return ErrNotFound
	}
	if current.Revision != expectedRunRevision || next.Revision != expectedRunRevision+1 ||
		record.Revision != expectedContextRevision+1 {
		return ErrCASMismatch
	}
	if current.Status != reportrun.StatusCompleted || next.Status != reportrun.StatusCompleted ||
		strings.TrimSpace(current.Origin) != "manual" ||
		strings.TrimSpace(current.ConversationID) != "" {
		return ErrImmutable
	}
	conversationID := strings.TrimSpace(next.ConversationID)
	if conversationID == "" ||
		strings.TrimSpace(record.ConversationID) != conversationID ||
		strings.TrimSpace(record.ActiveReportRunID) != strings.TrimSpace(next.ReportRunID) ||
		strings.TrimSpace(record.OwnerID) != strings.TrimSpace(next.OwnerID) ||
		strings.TrimSpace(next.OwnerID) != strings.TrimSpace(current.OwnerID) ||
		strings.TrimSpace(next.ReportRunID) != strings.TrimSpace(current.ReportRunID) ||
		strings.TrimSpace(next.UIRunRequestID) != strings.TrimSpace(current.UIRunRequestID) {
		return ErrNotFound
	}
	if strings.TrimSpace(next.AdoptionSource) == "" ||
		strings.TrimSpace(next.ActorID) != strings.TrimSpace(next.OwnerID) ||
		strings.TrimSpace(record.ActivationSource) == "" ||
		strings.TrimSpace(record.ActorID) != strings.TrimSpace(record.OwnerID) {
		return ErrImmutable
	}
	left := *current
	right := *next
	left.ConversationID, right.ConversationID = "", ""
	left.AdoptionSource, right.AdoptionSource = "", ""
	left.ActorID, right.ActorID = "", ""
	left.UpdatedAt, right.UpdatedAt = right.UpdatedAt, right.UpdatedAt
	left.Revision, right.Revision = right.Revision, right.Revision
	if !reflect.DeepEqual(left, right) {
		return ErrImmutable
	}
	return nil
}

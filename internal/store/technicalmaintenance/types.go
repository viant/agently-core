package technicalmaintenance

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/store/maintenancelease"
)

var ErrInvalidRequest = errors.New("invalid technical maintenance request")

const (
	Interactive  = "interactive"
	Scheduled    = "scheduled"
	Unclassified = "unclassified"
	ReportRun    = "report_run"
	ExportJob    = "report_export_job"
	Audit        = "report_audit_event"
	Session      = "session"
	DryRun       = "dry_run"
	Delete       = "delete"
)

type CandidateRequest struct {
	Scope                  string
	OlderThan, EvaluatedAt time.Time
	AfterCursor            string
	Limit                  int
}
type Candidate struct {
	CursorID, Kind, Scope, RecordID string
	ObservedAt                      time.Time
}
type Request struct {
	Kind        string                 `parameter:"Kind,kind=body,in=kind,required=true"`
	Scope       string                 `parameter:"Scope,kind=body,in=scope,required=true"`
	RecordID    string                 `parameter:"RecordID,kind=body,in=recordId,required=true"`
	OlderThan   time.Time              `parameter:"OlderThan,kind=body,in=olderThan,required=true"`
	EvaluatedAt time.Time              `parameter:"EvaluatedAt,kind=body,in=evaluatedAt,required=true"`
	Mode        string                 `parameter:"Mode,kind=body,in=mode,required=true"`
	Lease       maintenancelease.Lease `parameter:"Lease,kind=body,in=lease"`
}
type Result struct {
	Kind, Scope, RecordID, Mode string
	Eligible, Deleted           bool
	DeletedRows                 int64
	Reason                      string
}
type rule struct {
	kind     string
	priority int
}

var rules = []rule{{ReportRun, 10}, {ExportJob, 20}, {Audit, 30}, {Session, 40}}

const cursorSeparator = "\x1c"

func validScope(scope string) bool {
	return scope == Interactive || scope == Scheduled || scope == Unclassified
}
func ruleFor(kind string) (rule, bool) {
	for _, r := range rules {
		if r.kind == kind {
			return r, true
		}
	}
	return rule{}, false
}
func cursor(r rule, id string) string {
	return fmt.Sprintf("%04d%s%s%s%s", r.priority, cursorSeparator, r.kind, cursorSeparator, id)
}
func decodeCursor(value string) (int, string, string, error) {
	if value == "" {
		return 0, "", "", nil
	}
	parts := strings.SplitN(value, cursorSeparator, 3)
	if len(parts) != 3 || strings.TrimSpace(parts[2]) == "" {
		return 0, "", "", fmt.Errorf("%w: invalid technical maintenance cursor", ErrInvalidRequest)
	}
	priority, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", "", fmt.Errorf("%w: invalid technical maintenance cursor", ErrInvalidRequest)
	}
	if r, ok := ruleFor(parts[1]); ok && r.priority == priority {
		return priority, parts[1], parts[2], nil
	}
	return 0, "", "", fmt.Errorf("%w: unknown technical maintenance cursor rule", ErrInvalidRequest)
}
func validTimes(scope string, before, at time.Time) error {
	if !validScope(scope) {
		return fmt.Errorf("%w: unsupported technical maintenance scope %q", ErrInvalidRequest, scope)
	}
	if before.IsZero() || at.IsZero() {
		return fmt.Errorf("%w: technical retention cutoff and evaluation time are required", ErrInvalidRequest)
	}
	if before.After(at) {
		return fmt.Errorf("%w: technical retention cutoff cannot be after evaluation time", ErrInvalidRequest)
	}
	return nil
}
func validateRequest(request Request) error {
	if _, ok := ruleFor(request.Kind); !ok {
		return fmt.Errorf("%w: unsupported technical maintenance kind %q", ErrInvalidRequest, request.Kind)
	}
	if err := validTimes(request.Scope, request.OlderThan, request.EvaluatedAt); err != nil {
		return err
	}
	if request.Kind == Session && request.Scope != Unclassified {
		return fmt.Errorf("%w: sessions belong to unclassified technical maintenance", ErrInvalidRequest)
	}
	if strings.TrimSpace(request.RecordID) == "" {
		return fmt.Errorf("%w: technical record id is required", ErrInvalidRequest)
	}
	if request.Mode != DryRun && request.Mode != Delete {
		return fmt.Errorf("%w: unsupported technical maintenance mode %q", ErrInvalidRequest, request.Mode)
	}
	if request.Mode == Delete && (strings.TrimSpace(request.Lease.Key) == "" || strings.TrimSpace(request.Lease.OwnerID) == "" || strings.TrimSpace(request.Lease.Token) == "") {
		return fmt.Errorf("%w: technical delete requires maintenance lease", ErrInvalidRequest)
	}
	return nil
}

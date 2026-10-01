package orphanmaintenance

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	maintenance "github.com/viant/agently-core/internal/store/maintenancelease"
)

var ErrInvalidRequest = errors.New("invalid conversation maintenance request")

const (
	cursorSeparator      = "\x1e"
	cursorOrderSeparator = "\x1d"
	RecordSeparator      = "\x1f"
)

type CandidateRequest struct {
	OlderThan   time.Time
	AfterCursor string
	Limit       int
}
type Candidate struct {
	CursorID       string
	RuleID         string
	Action         Action
	Table          string
	RecordID       string
	ReferenceTable string
	ReferenceID    string
	ObservedAt     time.Time
}
type Request struct {
	RuleID    string
	RecordID  string
	OlderThan time.Time
	Lease     maintenance.Lease
}
type Reason string

const (
	Deleted          Reason = "deleted"
	Detached         Reason = "detached"
	ReportedOnly     Reason = "report_only"
	NoLongerEligible Reason = "no_longer_eligible"
)

type Result struct {
	RuleID   string
	RecordID string
	Action   Action
	Eligible bool
	Mutated  bool
	Deleted  bool
	Detached bool
	Reason   Reason
}

func EncodeCursor(rule Rule, recordID string) string {
	return fmt.Sprintf("%06d%s%s%s%s", rule.Priority, cursorOrderSeparator, rule.ID, cursorSeparator, recordID)
}
func DecodeCursor(cursor string, rules []Rule) (int, string, string, error) {
	if cursor == "" {
		return 0, "", "", nil
	}
	parts := strings.SplitN(cursor, cursorSeparator, 2)
	orderAndRule := strings.SplitN(parts[0], cursorOrderSeparator, 2)
	if len(parts) != 2 || len(orderAndRule) != 2 || strings.TrimSpace(orderAndRule[0]) == "" || strings.TrimSpace(orderAndRule[1]) == "" || strings.TrimSpace(parts[1]) == "" {
		return 0, "", "", fmt.Errorf("%w: invalid orphan candidate cursor", ErrInvalidRequest)
	}
	priority, err := strconv.Atoi(orderAndRule[0])
	if err != nil || priority <= 0 {
		return 0, "", "", fmt.Errorf("%w: invalid orphan candidate cursor", ErrInvalidRequest)
	}
	for _, rule := range rules {
		if rule.ID == orderAndRule[1] {
			if rule.Priority != priority {
				return 0, "", "", fmt.Errorf("%w: orphan cursor priority does not match rule %q", ErrInvalidRequest, rule.ID)
			}
			return priority, rule.ID, parts[1], nil
		}
	}
	return 0, "", "", fmt.Errorf("%w: orphan cursor references unknown rule %q", ErrInvalidRequest, orderAndRule[1])
}
func KeyValues(rule Rule, recordID string) ([]string, error) {
	parts := strings.Split(recordID, RecordSeparator)
	if len(parts) != len(rule.Keys) {
		return nil, fmt.Errorf("%w: orphan record id does not match rule %q key", ErrInvalidRequest, rule.ID)
	}
	return parts, nil
}
func normalizeRequest(request Request) (Request, error) {
	request.RuleID = strings.TrimSpace(request.RuleID)
	request.RecordID = strings.TrimSpace(request.RecordID)
	request.OlderThan = request.OlderThan.UTC()
	if request.RuleID == "" {
		return request, fmt.Errorf("%w: orphan rule id is required", ErrInvalidRequest)
	}
	if request.RecordID == "" {
		return request, fmt.Errorf("%w: orphan record id is required", ErrInvalidRequest)
	}
	if request.OlderThan.IsZero() {
		return request, fmt.Errorf("%w: orphan grace-period cutoff is required", ErrInvalidRequest)
	}
	request.Lease.Key = strings.TrimSpace(request.Lease.Key)
	request.Lease.OwnerID = strings.TrimSpace(request.Lease.OwnerID)
	request.Lease.Token = strings.TrimSpace(request.Lease.Token)
	request.Lease.LeaseUntil = request.Lease.LeaseUntil.UTC()
	if request.Lease.Key == "" || request.Lease.OwnerID == "" || request.Lease.Token == "" {
		return request, fmt.Errorf("%w: orphan maintenance requires maintenance lease: key, owner id and token are required", ErrInvalidRequest)
	}
	return request, nil
}

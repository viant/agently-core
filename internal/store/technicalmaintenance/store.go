package technicalmaintenance

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/datly/queryselectors"
	auditread "github.com/viant/agently-core/internal/datly/reporting/audit/read"
	jobread "github.com/viant/agently-core/internal/datly/reporting/job/read"
	runread "github.com/viant/agently-core/internal/datly/reporting/run/read"
	sessionread "github.com/viant/agently-core/internal/datly/session/read"

	datlypredicate "github.com/viant/agently-core/internal/datly/predicate"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/state"
)

type Store struct{ Invoker dexec.ComponentInvoker }

func providers(access string, policy *datlypredicate.TechnicalRetention, lock bool) []locator.Provider {
	owner := ""
	return []locator.Provider{
		provider.Named(access, func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			switch name {
			case "internal":
				return true, true, nil
			case "lock":
				return lock, true, nil
			}
			return nil, false, nil
		}),
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }),
		provider.Named("technicalmaintenance", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			switch name {
			case "policy":
				if policy != nil {
					return policy, true, nil
				}
			case "deleteAudit":
				return true, true, nil
			case "cutoff":
				if policy != nil {
					return policy.OlderThan.UTC(), true, nil
				}
			}
			return nil, false, nil
		}),
	}
}
func (s *Store) List(ctx context.Context, request CandidateRequest) ([]Candidate, error) {
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("technical maintenance invoker is required")
	}
	request.OlderThan = request.OlderThan.UTC()
	request.EvaluatedAt = request.EvaluatedAt.UTC()
	request.AfterCursor = strings.TrimSpace(request.AfterCursor)
	if err := validTimes(request.Scope, request.OlderThan, request.EvaluatedAt); err != nil {
		return nil, err
	}
	if request.Limit <= 0 {
		return nil, fmt.Errorf("%w: technical candidate limit must be positive", ErrInvalidRequest)
	}
	afterPriority, afterKind, afterID, err := decodeCursor(request.AfterCursor)
	if err != nil {
		return nil, err
	}
	result := make([]Candidate, 0, request.Limit)
	for _, rule := range rules {
		if rule.kind == Session && request.Scope != Unclassified {
			continue
		}
		if request.AfterCursor != "" && (rule.priority < afterPriority || (rule.priority == afterPriority && rule.kind < afterKind)) {
			continue
		}
		after := ""
		if request.AfterCursor != "" && rule.priority == afterPriority && rule.kind == afterKind {
			after = afterID
		}
		page, err := s.candidates(ctx, rule, CandidateRequest{Scope: request.Scope, OlderThan: request.OlderThan, EvaluatedAt: request.EvaluatedAt, Limit: request.Limit - len(result)}, after, "", false)
		if err != nil {
			return nil, err
		}
		result = append(result, page...)
		if len(result) == request.Limit {
			break
		}
	}
	return result, nil
}
func (s *Store) candidates(ctx context.Context, r rule, request CandidateRequest, after, exact string, lock bool) ([]Candidate, error) {
	policy := &datlypredicate.TechnicalRetention{Scope: request.Scope, OlderThan: request.OlderThan.UTC(), EvaluatedAt: request.EvaluatedAt.UTC(), AfterRecord: after, ExactRecord: exact}
	result := make([]Candidate, 0, request.Limit)
	add := func(id, observed string) {
		if at, ok := parseObserved(observed); ok {
			result = append(result, Candidate{CursorID: cursor(r, id), Kind: r.kind, Scope: request.Scope, RecordID: id, ObservedAt: at})
		}
	}
	switch r.kind {
	case ReportRun:
		input := &runread.Input{}
		out, err := retentionRead[runread.Output](ctx, s, input, "/v1/internal/forge/reporting/run", providers("reportaccess", policy, lock), []string{"report_run_id", "maintenance_observed_at"}, "report_run_id ASC", request.Limit)
		if err != nil {
			return nil, err
		}
		for _, row := range out.Data {
			if row != nil {
				add(row.ReportRunId, row.MaintenanceObservedAt)
			}
		}
	case ExportJob:
		input := &jobread.Input{}
		out, err := retentionRead[jobread.Output](ctx, s, input, "/v1/internal/forge/reporting/job", providers("reportaccess", policy, lock), []string{"job_id", "maintenance_observed_at"}, "job_id ASC", request.Limit)
		if err != nil {
			return nil, err
		}
		for _, row := range out.Data {
			if row != nil {
				add(row.JobId, row.MaintenanceObservedAt)
			}
		}
	case Audit:
		input := &auditread.Input{}
		out, err := retentionRead[auditread.Output](ctx, s, input, "/v1/internal/forge/reporting/audit", providers("reportauditaccess", policy, lock), []string{"event_id", "maintenance_observed_at"}, "event_id ASC", request.Limit)
		if err != nil {
			return nil, err
		}
		for _, row := range out.Data {
			if row != nil {
				add(row.EventId, row.MaintenanceObservedAt)
			}
		}
	case Session:
		input := &sessionread.SessionInput{}
		out, err := retentionRead[sessionread.SessionOutput](ctx, s, input, "/v1/api/agently/user/session", providers("sessionmaintenance", policy, lock), []string{"id", "maintenance_observed_at"}, "id ASC", request.Limit)
		if err != nil {
			return nil, err
		}
		for _, row := range out.Data {
			if row != nil {
				add(row.Id, row.MaintenanceObservedAt)
			}
		}
	default:
		return nil, fmt.Errorf("%w: unsupported technical maintenance kind", ErrInvalidRequest)
	}
	return result, nil
}
func parseObserved(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "0000-00-00 00:00:00") {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), true
		}
		if parsed, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func retentionRead[Output any](ctx context.Context, s *Store, input any, path string, bindings []locator.Provider, fields []string, order string, limit int) (*Output, error) {
	if len(fields) > 0 || order != "" || limit > 0 {
		bindings = append(bindings, queryselectors.Provider(state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: fields, OrderBy: order, Limit: limit}}}))
	}
	target := dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeOf(input).Elem().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: path}}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: target, Input: input, Providers: bindings})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("technical reader %s returned %T", path, value)
	}
	return out, nil
}

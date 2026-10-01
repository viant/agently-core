package predicate

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/viant/datly/sql/fragment"
	"github.com/viant/xdatly/predicate"
)

// TechnicalRetention is supplied only by the trusted retention orchestration.
// Predicates keep cutoff, TTL, reference guards and binary cursors in SQL.
type TechnicalRetention struct {
	Scope       string
	OlderThan   time.Time
	EvaluatedAt time.Time
	AfterRecord string
	ExactRecord string
}

type TechnicalReportRunRetention struct{}
type TechnicalReportJobRetention struct{}
type TechnicalReportAuditRetention struct{}
type TechnicalSessionRetention struct{}

var LinkedTechnicalRetentionTypes = []reflect.Type{reflect.TypeFor[TechnicalRetention](), reflect.TypeFor[TechnicalReportRunRetention](), reflect.TypeFor[TechnicalReportJobRetention](), reflect.TypeFor[TechnicalReportAuditRetention](), reflect.TypeFor[TechnicalSessionRetention]()}

func technicalPolicy(ctx context.Context, value any) (*TechnicalRetention, string, error) {
	policy, ok := value.(*TechnicalRetention)
	if !ok || policy == nil || policy.OlderThan.IsZero() || policy.EvaluatedAt.IsZero() || policy.OlderThan.After(policy.EvaluatedAt) {
		return nil, "", fmt.Errorf("technical retention requires valid cutoff/evaluation policy")
	}
	if policy.Scope != "interactive" && policy.Scope != "scheduled" && policy.Scope != "unclassified" {
		return nil, "", fmt.Errorf("unsupported technical retention scope")
	}
	dialect := fragment.Dialect(ctx)
	if dialect == nil {
		return nil, "", fmt.Errorf("technical retention dialect is unavailable")
	}
	name := strings.ToLower(dialect.Product.Name)
	if !strings.Contains(name, "sqlite") && !strings.Contains(name, "mysql") {
		return nil, "", fmt.Errorf("unsupported technical retention dialect %s", dialect.Product.Name)
	}
	return policy, name, nil
}
func technicalScope(scope, conversation string) string {
	scheduled := `(COALESCE(technical_conversation.scheduled,0) <> 0 OR TRIM(COALESCE(technical_conversation.schedule_id,'')) <> '' OR TRIM(COALESCE(technical_conversation.schedule_run_id,'')) <> '' OR TRIM(COALESCE(technical_conversation.schedule_kind,'')) <> '' OR EXISTS(SELECT 1 FROM run technical_run WHERE technical_run.conversation_id=technical_conversation.id AND LOWER(TRIM(COALESCE(technical_run.conversation_kind,'')))='scheduled'))`
	base := "SELECT 1 FROM conversation technical_conversation WHERE technical_conversation.id=" + conversation
	switch scope {
	case "interactive":
		return "EXISTS(" + base + " AND NOT " + scheduled + ")"
	case "scheduled":
		return "EXISTS(" + base + " AND " + scheduled + ")"
	default:
		return "NOT EXISTS(" + base + ")"
	}
}
func technicalTTL(dialect, alias, age string) string {
	ttl := "COALESCE(" + alias + ".retention_ttl_sec,0)"
	expiration := "TIMESTAMPADD(SECOND," + ttl + "," + age + ")"
	if strings.Contains(dialect, "sqlite") {
		expiration = "datetime(" + age + ",printf('+%d seconds'," + ttl + "))"
	}
	return "((" + ttl + ">0 AND " + expiration + "<=?) OR (" + ttl + "<=0 AND " + age + "<=?))"
}
func technicalFinish(policy *TechnicalRetention, dialect, identity, expression string, args []any) *predicate.Criteria {
	if policy.ExactRecord != "" {
		expression += " AND " + identity + "=?"
		args = append(args, policy.ExactRecord)
	} else if policy.AfterRecord != "" {
		if strings.Contains(dialect, "mysql") {
			expression += " AND BINARY CAST(" + identity + " AS CHAR)>BINARY ?"
		} else {
			expression += " AND CAST(" + identity + " AS TEXT) COLLATE BINARY > ? COLLATE BINARY"
		}
		args = append(args, policy.AfterRecord)
	}
	return &predicate.Criteria{Expression: expression, Placeholders: args}
}
func (*TechnicalReportRunRetention) Compute(ctx context.Context, value any) (*predicate.Criteria, error) {
	policy, dialect, err := technicalPolicy(ctx, value)
	if err != nil {
		return nil, err
	}
	jobExpired := technicalTTL(dialect, "dep_job", "COALESCE(dep_job.completed_at,dep_job.started_at,dep_job.submitted_at)")
	artifactExpired := technicalTTL(dialect, "dep_artifact", "dep_artifact.created_at")
	expression := `r.updated_at<=? AND ` + technicalScope(policy.Scope, "r.conversation_id") + ` AND NOT EXISTS(SELECT 1 FROM conversation_report_context crc WHERE crc.owner_id=r.owner_id AND crc.active_report_run_id=r.report_run_id AND crc.updated_at>?) AND NOT EXISTS(SELECT 1 FROM report_export_job dep_job WHERE dep_job.report_run_id=r.report_run_id AND (NOT (` + jobExpired + `) OR EXISTS(SELECT 1 FROM report_export_artifact dep_artifact WHERE dep_artifact.job_id=dep_job.job_id AND NOT (` + artifactExpired + `)) OR EXISTS(SELECT 1 FROM report_audit_event dep_audit WHERE (dep_audit.job_id=dep_job.job_id OR EXISTS(SELECT 1 FROM report_export_artifact audit_artifact WHERE audit_artifact.job_id=dep_job.job_id AND audit_artifact.artifact_id=dep_audit.artifact_id)) AND dep_audit.occurred_at>?)))`
	args := []any{policy.OlderThan.UTC(), policy.OlderThan.UTC(), policy.EvaluatedAt.UTC(), policy.OlderThan.UTC(), policy.EvaluatedAt.UTC(), policy.OlderThan.UTC(), policy.OlderThan.UTC()}
	return technicalFinish(policy, dialect, "r.report_run_id", expression, args), nil
}
func (*TechnicalReportJobRetention) Compute(ctx context.Context, value any) (*predicate.Criteria, error) {
	policy, dialect, err := technicalPolicy(ctx, value)
	if err != nil {
		return nil, err
	}
	conversation := `COALESCE(NULLIF(j.conversation_id,''),(SELECT NULLIF(technical_run.conversation_id,'') FROM report_run technical_run WHERE technical_run.report_run_id=j.report_run_id))`
	expression := technicalTTL(dialect, "j", "COALESCE(j.completed_at,j.started_at,j.submitted_at)") + " AND " + technicalScope(policy.Scope, conversation) + ` AND NOT EXISTS(SELECT 1 FROM report_export_artifact dep_artifact WHERE dep_artifact.job_id=j.job_id AND NOT (` + technicalTTL(dialect, "dep_artifact", "dep_artifact.created_at") + `)) AND NOT EXISTS(SELECT 1 FROM report_audit_event dep_audit WHERE (dep_audit.job_id=j.job_id OR EXISTS(SELECT 1 FROM report_export_artifact audit_artifact WHERE audit_artifact.job_id=j.job_id AND audit_artifact.artifact_id=dep_audit.artifact_id)) AND dep_audit.occurred_at>?)`
	args := []any{policy.EvaluatedAt.UTC(), policy.OlderThan.UTC(), policy.EvaluatedAt.UTC(), policy.OlderThan.UTC(), policy.OlderThan.UTC()}
	return technicalFinish(policy, dialect, "j.job_id", expression, args), nil
}
func (*TechnicalReportAuditRetention) Compute(ctx context.Context, value any) (*predicate.Criteria, error) {
	policy, dialect, err := technicalPolicy(ctx, value)
	if err != nil {
		return nil, err
	}
	conversation := `COALESCE((SELECT NULLIF(dj.conversation_id,'') FROM report_export_job dj WHERE dj.job_id=a.job_id),(SELECT NULLIF(dr.conversation_id,'') FROM report_export_job dj JOIN report_run dr ON dr.report_run_id=dj.report_run_id WHERE dj.job_id=a.job_id),(SELECT NULLIF(aj.conversation_id,'') FROM report_export_artifact la JOIN report_export_job aj ON aj.job_id=la.job_id WHERE la.artifact_id=a.artifact_id),(SELECT NULLIF(ar.conversation_id,'') FROM report_export_artifact la JOIN report_export_job aj ON aj.job_id=la.job_id JOIN report_run ar ON ar.report_run_id=aj.report_run_id WHERE la.artifact_id=a.artifact_id))`
	return technicalFinish(policy, dialect, "a.event_id", "a.occurred_at<=? AND "+technicalScope(policy.Scope, conversation), []any{policy.OlderThan.UTC()}), nil
}
func (*TechnicalSessionRetention) Compute(ctx context.Context, value any) (*predicate.Criteria, error) {
	policy, dialect, err := technicalPolicy(ctx, value)
	if err != nil {
		return nil, err
	}
	if policy.Scope != "unclassified" {
		return nil, fmt.Errorf("technical sessions require unclassified scope")
	}
	return technicalFinish(policy, dialect, "s.id", "s.expires_at<=?", []any{policy.OlderThan.UTC()}), nil
}

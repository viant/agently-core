// Package reportingevidence persists immutable JSON evidence attached to an execution run.
package reportingevidence

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"

	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

const Admission = "agently.forecast.admission.v1"
const Plan = "agently.forecast.plan.v1"

type Scope struct{ OwnerID, ConversationID, TurnID, RunID string }
type Request struct {
	Scope
	Operation  string
	Subtype    string
	PlanID     string
	LeaseOwner string
	Body       json.RawMessage
}
type Response struct{ Body json.RawMessage }
type Documents interface {
	Load(context.Context, Scope, string, string) (json.RawMessage, error)
	Save(context.Context, Scope, string, string, string, json.RawMessage) error
}
type Store struct{ Invoker dexec.ComponentInvoker }

func New(invoker dexec.ComponentInvoker) *Store { return &Store{Invoker: invoker} }
func Schema(subtype string) string              { return "urn:" + subtype }
func ID(scope Scope, subtype, planID string) string {
	h := sha256.New()
	h.Write([]byte("agently.evidence.v1"))
	var size [8]byte
	for _, part := range []string{scope.OwnerID, scope.ConversationID, scope.TurnID, scope.RunID, subtype, planID} {
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		h.Write(size[:])
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}
func Validate(r *Request) error {
	if r == nil || r.OwnerID == "" || r.ConversationID == "" || r.TurnID == "" || r.RunID == "" {
		return fmt.Errorf("evidence scope required")
	}
	if (r.Subtype != Admission && r.Subtype != Plan) || (r.Subtype == Admission && r.PlanID != "") || (r.Subtype == Plan && r.PlanID == "") {
		return fmt.Errorf("evidence document identity invalid")
	}
	if r.Operation != "load" && r.Operation != "save" {
		return fmt.Errorf("evidence operation invalid")
	}
	if r.Operation == "save" && (r.LeaseOwner == "" || !json.Valid(r.Body)) {
		return fmt.Errorf("evidence write requires lease and JSON")
	}
	return nil
}
func (s *Store) invoke(ctx context.Context, r *Request) (*Response, error) {
	if err := Validate(r); err != nil {
		return nil, err
	}
	if s == nil || s.Invoker == nil {
		return nil, fmt.Errorf("evidence store unavailable")
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{
		Component: spec.Key{Kind: spec.KindComponent, Scope: "github.com/viant/agently-core/internal/datly/reportingevidence/manage", Name: "Manage"},
		Route:     spec.RouteRef{Method: "POST", Path: "/v1/internal/agently/reporting-evidence/manage"}}, Input: r})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*Response)
	if !ok || out == nil {
		return nil, fmt.Errorf("evidence store returned %T", value)
	}
	return out, nil
}
func (s *Store) Load(ctx context.Context, scope Scope, subtype, planID string) (json.RawMessage, error) {
	out, err := s.invoke(ctx, &Request{Scope: scope, Operation: "load", Subtype: subtype, PlanID: planID})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}
func (s *Store) Save(ctx context.Context, scope Scope, subtype, planID, lease string, body json.RawMessage) error {
	_, err := s.invoke(ctx, &Request{Scope: scope, Operation: "save", Subtype: subtype, PlanID: planID, LeaseOwner: lease, Body: body})
	return err
}

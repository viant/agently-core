package forecastbinding

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"

	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/mcpname"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

const sourceProfileReference = "sourceProfileOpId"
const trustedProfileReference = "evidenceSourceOpId"

// OperationCatalog returns only completed operation identities in the exact
// authorized turn. Ordering never chooses a profile or plan.
type OperationCatalog interface {
	CompletedOperations(context.Context, Scope, string) ([]string, error)
}

// Controller is a server-created turn capability. Lease is resolved at write
// time because an execution heartbeat may replace the original lease token.
type Controller struct {
	runtime     *Runtime
	scope       Scope
	lease       func() string
	active      atomic.Bool
	planMu      sync.Mutex
	planIDs     map[string]bool
	publication *Guard
}

func NewController(runtime *Runtime, scope Scope, lease func() string) (*Controller, error) {
	if runtime == nil || lease == nil || scope.OwnerID == "" || scope.ConversationID == "" || scope.TurnID == "" {
		return nil, reject("controller dependencies or scope missing")
	}
	controller := &Controller{runtime: runtime, scope: scope, lease: lease}
	controller.publication = newGuard(runtime, scope, controller.check, controller.active.Load)
	return controller, nil
}

func (c *Controller) check(ctx context.Context) error {
	turn, ok := requestctx.TurnMetaFromContext(ctx)
	if !ok || turn.ConversationID != c.scope.ConversationID || turn.TurnID != c.scope.TurnID || authctx.EffectiveUserID(ctx) != c.scope.OwnerID {
		return reject("controller authenticated turn mismatch")
	}
	return nil
}

func (c *Controller) Prepare(ctx context.Context, tool, _ string, raw json.RawMessage) (json.RawMessage, bool, error) {
	if !strings.EqualFold(mcpname.Display(tool), "steward/ForecastingTargetingConvert") {
		return nil, false, nil
	}
	if err := c.check(ctx); err != nil {
		return nil, true, err
	}
	request, err := object(raw)
	if err != nil {
		return nil, true, err
	}
	args, ok := request["Request"].(map[string]any)
	if !ok {
		return nil, true, reject("conversion request missing")
	}
	if _, supplied := args[trustedProfileReference]; supplied {
		return nil, true, reject("model cannot supply trusted source reference")
	}
	var candidates []string
	if ref, supplied := args[sourceProfileReference]; supplied {
		id, ok := ref.(string)
		if !ok || id == "" {
			return nil, true, reject("invalid profile operation reference")
		}
		candidates = []string{id}
		delete(args, sourceProfileReference)
	} else {
		catalog, ok := c.runtime.store.(OperationCatalog)
		if !ok {
			return nil, true, reject("profile operation catalog unavailable")
		}
		candidates, err = catalog.CompletedOperations(ctx, c.scope, "steward/AdTargetingProfile")
		if err != nil {
			return nil, true, err
		}
	}
	clean, err := json.Marshal(request)
	if err != nil {
		return nil, true, err
	}
	admission, err := c.runtime.store.LoadAdmission(ctx, c.scope)
	if err != nil {
		return nil, true, err
	}
	if admission == nil || admission.Scope != c.scope {
		return nil, true, reject("admission scope mismatch")
	}
	if err = admission.Validate(); err != nil {
		return nil, true, err
	}
	var selected json.RawMessage
	var selectedID string
	seen := map[string]bool{}
	for _, id := range candidates {
		if seen[id] {
			return nil, true, reject("duplicate profile operation")
		}
		seen[id] = true
		source, err := c.runtime.store.LoadCompletedCall(ctx, c.scope, id)
		if err != nil {
			return nil, true, err
		}
		prepared, err := prepareConversion(admission, source, c.scope, clean)
		if err != nil {
			continue
		}
		if selected != nil {
			return nil, true, reject("ambiguous profile; supply sourceProfileOpId")
		}
		selected, selectedID = prepared, id
	}
	if selected == nil {
		return nil, true, reject("no completed profile matches conversion and admitted selection")
	}
	effective, err := object(selected)
	if err != nil {
		return nil, true, err
	}
	effective["Request"].(map[string]any)[trustedProfileReference] = selectedID
	result, err := json.Marshal(effective)
	return result, true, err
}

func (c *Controller) Completed(ctx context.Context, tool, op string) (json.RawMessage, bool, error) {
	switch strings.ToLower(mcpname.Display(tool)) {
	case "steward/adtargetingprofile", "steward/forecastingtargetingconvert", "steward/forecastingcube":
	default:
		return nil, false, nil
	}
	if err := c.check(ctx); err != nil {
		return nil, true, err
	}
	body, err := c.runtime.ProjectCompleted(ctx, c.scope, op, c.lease())
	if err == nil && strings.EqualFold(mcpname.Display(tool), "steward/ForecastingTargetingConvert") {
		err = c.rememberPlan(body)
	}
	return body, true, err
}

// RestorePlans replays only server-enriched, completed conversion records. A
// crash between tool completion and receipt publication may be repaired from
// those exact immutable inputs under the newly claimed execution lease.
func (c *Controller) RestorePlans(ctx context.Context) error {
	if err := c.check(ctx); err != nil {
		return err
	}
	catalog, ok := c.runtime.store.(OperationCatalog)
	if !ok {
		return reject("conversion operation catalog unavailable")
	}
	operations, err := catalog.CompletedOperations(ctx, c.scope, "steward/ForecastingTargetingConvert")
	if err != nil {
		return err
	}
	for _, op := range operations {
		call, err := c.runtime.store.LoadCompletedCall(ctx, c.scope, op)
		if err != nil {
			return err
		}
		request, err := object(call.Request)
		if err != nil {
			return err
		}
		args, _ := request["Request"].(map[string]any)
		source, _ := args[trustedProfileReference].(string)
		if source == "" {
			continue
		}
		body, projectErr := c.runtime.ProjectCompleted(ctx, c.scope, op, c.lease())
		if projectErr != nil {
			err = projectErr
			return err
		}
		if err = c.rememberPlan(body); err != nil {
			return err
		}
	}
	return nil
}

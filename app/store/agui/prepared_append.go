package agui

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
)

// AppendPreparation prepares projection changes against the locked current
// thread. Preparation must not perform mutations or external I/O; append
// retains the original CAS.
type AppendPreparation func(*Thread) ([]json.RawMessage, *Change, error)

// PreparedAppender keeps the thread read, projection rebase and append in one
// native transaction. Other Store implementations retain the ordinary CAS API.
type PreparedAppender interface {
	// OwnsPreparedAppend prevents promoted methods from bypassing an outer
	// store decorator. A decorator must explicitly opt in as its own identity.
	OwnsPreparedAppend(Store) bool
	AppendPrepared(context.Context, string, string, string, int64, AppendPreparation) (*Run, error)
}

func (s *ComponentStore) AppendPrepared(ctx context.Context, principal, threadID, runID string, expectedRevision int64, prepare AppendPreparation) (*Run, error) {
	if s == nil || s.Invoker == nil || prepare == nil {
		return nil, fmt.Errorf("AG-UI append preparation is required")
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{
		Target:    manageTarget,
		Input:     &Request{Operation: "preparedAppend", Principal: principal, ThreadID: threadID, RunID: runID, ExpectedRevision: expectedRevision},
		Providers: []locator.Provider{provider.Static("aguiAppendPreparation", prepare)},
	})
	if err != nil {
		return nil, err
	}
	output, ok := value.(*Response)
	if !ok || output == nil {
		return nil, fmt.Errorf("AG-UI prepared append returned %T", value)
	}
	return output.Run, nil
}

func (s *ComponentStore) OwnsPreparedAppend(caller Store) bool {
	self, ok := caller.(*ComponentStore)
	return ok && self == s
}

package toolapprovalqueue

import (
	"context"

	queueRead "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/read"
	"github.com/viant/datly"
)

// Source of truth: dql/agently/toolapprovalqueue/list.dql
//
// This package is a compatibility shim for older imports that still refer to
// the historical "pending" queue component. Runtime and tests should treat
// pkg/agently/toolapprovalqueue/read as the canonical Datly contract.

type QueueRowsInput = queueRead.QueueRowsInput
type QueueRowsInputHas = queueRead.QueueRowsInputHas
type QueueRowsOutput = queueRead.QueueRowsOutput
type QueueRowsView = queueRead.QueueRowView

var QueueRowsPathURI = queueRead.QueueRowsPathURI

func DefineQueueRowsComponent(ctx context.Context, srv *datly.Service) error {
	return queueRead.DefineQueueRowsComponent(ctx, srv)
}

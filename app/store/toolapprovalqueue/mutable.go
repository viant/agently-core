package toolapprovalqueue

import queuew "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/write"

// NewToolApprovalQueue allocates a mutable queue row with Has marker populated.
func NewToolApprovalQueue() *MutableToolApprovalQueue {
	v := &queuew.ToolApprovalQueue{Has: &queuew.ToolApprovalQueueHas{}}
	return (*MutableToolApprovalQueue)(v)
}

package toolapprovalqueue

import toolapprovalqueuemodel "github.com/viant/agently-core/model/toolapprovalqueue"

// NewToolApprovalQueue allocates a mutable queue row with Has marker populated.
func NewToolApprovalQueue() *MutableToolApprovalQueue {
	v := &toolapprovalqueuemodel.ToolApprovalQueue{Has: &toolapprovalqueuemodel.ToolApprovalQueueHas{}}
	return (*MutableToolApprovalQueue)(v)
}

package turnqueue

import turnqueuemodel "github.com/viant/agently-core/model/turnqueue"

// NewTurnQueue allocates a mutable queue row with Has marker populated.
func NewTurnQueue() *MutableTurnQueue {
	v := &turnqueuemodel.TurnQueue{Has: &turnqueuemodel.TurnQueueHas{}}
	return (*MutableTurnQueue)(v)
}

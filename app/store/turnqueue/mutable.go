package turnqueue

import queuew "github.com/viant/agently-core/pkg/agently/turnqueue/write"

// NewTurnQueue allocates a mutable queue row with Has marker populated.
func NewTurnQueue() *MutableTurnQueue {
	v := &queuew.TurnQueue{Has: &queuew.TurnQueueHas{}}
	return (*MutableTurnQueue)(v)
}

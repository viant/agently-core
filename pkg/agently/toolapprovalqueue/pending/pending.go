package toolapprovalqueue

import (
	queueRead "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/read"
)

// Historical import aliases preserve public Go data shapes.

type QueueRowsInput = queueRead.QueueRowsInput
type QueueRowsInputHas = queueRead.QueueRowsInputHas
type QueueRowsOutput = queueRead.QueueRowsOutput
type QueueRowsView = queueRead.QueueRowView

var QueueRowsPathURI = queueRead.QueueRowsPathURI

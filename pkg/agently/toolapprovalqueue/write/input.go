package write

import "embed"

//go:embed sql/*.sql
var FS embed.FS

type Input struct {
	Queues []*ToolApprovalQueue `parameter:",kind=body,in=data"`
}

func (i *Input) EmbedFS() (fs *embed.FS) { return &FS }

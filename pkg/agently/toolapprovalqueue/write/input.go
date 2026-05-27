package write

import "embed"

//go:embed sql/*.sql
var FS embed.FS

type Input struct {
	Queues []*ToolApprovalQueue `parameter:",kind=body,in=data"`

	CurIDs *struct{ Values []string } `parameter:",kind=param,in=Queues,dataType=toolapprovalqueue/write.ToolApprovalQueues" codec:"structql,uri=sql/cur_ids.sql"`

	Cur []*ToolApprovalQueue `parameter:",kind=view,in=Cur" view:"Cur" sql:"uri=sql/cur_tool_approval_queue.sql"`

	CurByID map[string]*ToolApprovalQueue
}

func (i *Input) EmbedFS() (fs *embed.FS) { return &FS }

package write

import "embed"

//go:embed sql/*.sql
var FS embed.FS

type Input struct {
	Goals []*Goal `parameter:",kind=body,in=data"`

	CurGoalIDs *struct{ Values []string } `parameter:",kind=param,in=Goals,dataType=goal/write.MutableGoalViews" codec:"structql,uri=sql/cur_ids.sql"`

	CurGoals []*Goal `parameter:",kind=view,in=CurGoals" view:"CurGoals" sql:"uri=sql/cur_goal.sql"`

	CurGoalById IndexedGoal
}

func (i *Input) EmbedFS() (fs *embed.FS) { return &FS }

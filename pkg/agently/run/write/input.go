package write

import "embed"

//go:embed sql/*.sql
var FS embed.FS

type Input struct {
	Runs []*MutableRunView `parameter:",kind=body,in=data"`

	CurIDs *struct{ Values []string } `parameter:",kind=param,in=Runs,dataType=run/write.MutableRunViews" codec:"structql,uri=sql/cur_ids.sql"`

	Cur []*MutableRunView `parameter:",kind=view,in=Cur" view:"Cur" sql:"uri=sql/cur_run.sql"`

	CurByID map[string]*MutableRunView
}

func (i *Input) EmbedFS() (fs *embed.FS) { return &FS }

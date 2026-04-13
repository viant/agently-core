package write

import "embed"

//go:embed sql/*.sql
var FS embed.FS

type Input struct {
	ModelCalls []*ModelCall `parameter:",kind=body,in=data"`
	CurByID    map[string]*ModelCall
}

func (i *Input) EmbedFS() (fs *embed.FS) { return &FS }

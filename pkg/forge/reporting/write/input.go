package write

import (
	"embed"

	forgereport "github.com/viant/agently-core/pkg/forge/reporting"
)

//go:embed sql/*.sql
var FS embed.FS

type Input struct {
	Artifact *forgereport.SharedArtifact `parameter:",kind=body,in=data"`
}

func (i *Input) EmbedFS() *embed.FS { return &FS }

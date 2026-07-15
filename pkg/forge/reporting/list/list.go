package list

import (
	"context"
	"embed"
	"fmt"
	"reflect"
	"strings"

	forgereport "github.com/viant/agently-core/pkg/forge/reporting"
	"github.com/viant/datly"
	"github.com/viant/datly/repository"
	"github.com/viant/datly/repository/contract"
	"github.com/viant/datly/view"
	"github.com/viant/xdatly/handler/response"
)

//go:embed sql/*.sql
var FS embed.FS

const PathURI = "/v1/api/forge/reporting/shared-artifact/list"

type Input struct {
	OwnerID     string    `parameter:",kind=query,in=ownerId" predicate:"equal,group=0,r,owner_id"`
	ArtifactRef string    `parameter:",kind=query,in=artifactRef" predicate:"equal,group=0,r,artifact_ref"`
	ReportID    string    `parameter:",kind=query,in=reportId" predicate:"equal,group=0,r,report_id"`
	Kind        string    `parameter:",kind=query,in=kind" predicate:"equal,group=0,r,kind"`
	Lifecycle   string    `parameter:",kind=query,in=lifecycle" predicate:"equal,group=0,r,lifecycle"`
	Has         *InputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type InputHas struct {
	OwnerID     bool
	ArtifactRef bool
	ReportID    bool
	Kind        bool
	Lifecycle   bool
}

type Output struct {
	response.Status `parameter:",kind=output,in=status" json:",omitempty"`
	Data            []*forgereport.SharedArtifact `parameter:",kind=output,in=view" view:"forge_report_shared_artifact_list,batch=5000,relationalConcurrency=1" sql:"uri=sql/shared_artifact_list.sql"`
	Metrics         response.Metrics              `parameter:",kind=output,in=metrics"`
}

func DefineComponent(ctx context.Context, srv *datly.Service, connectorRef string) error {
	normalizedConnector := strings.TrimSpace(connectorRef)
	if normalizedConnector == "" {
		normalizedConnector = "agently"
	}
	component, err := repository.NewComponent(
		contract.NewPath("GET", PathURI),
		repository.WithResource(srv.Resource()),
		repository.WithContract(
			reflect.TypeOf(Input{}),
			reflect.TypeOf(Output{}),
			&FS,
			view.WithConnectorRef(normalizedConnector),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to create forge reporting shared artifact list component: %w", err)
	}
	if err := srv.AddComponent(ctx, component); err != nil {
		return fmt.Errorf("failed to add forge reporting shared artifact list component: %w", err)
	}
	return nil
}

func (i *Input) EmbedFS() *embed.FS { return &FS }

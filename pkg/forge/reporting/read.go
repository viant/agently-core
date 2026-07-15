package reporting

import (
	"context"
	"embed"
	"fmt"
	"reflect"
	"strings"

	"github.com/viant/datly"
	"github.com/viant/datly/repository"
	"github.com/viant/datly/repository/contract"
	"github.com/viant/datly/view"
	"github.com/viant/xdatly/handler/response"
)

//go:embed sql/*.sql
var FS embed.FS

const SharedArtifactPathURI = "/v1/api/forge/reporting/shared-artifact/{artifactId}"

type SharedArtifactInput struct {
	ArtifactID string                  `parameter:",kind=path,in=artifactId" predicate:"equal,group=0,r,artifact_id"`
	OwnerID    string                  `parameter:",kind=query,in=ownerId" predicate:"equal,group=0,r,owner_id"`
	Has        *SharedArtifactInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type SharedArtifactInputHas struct {
	ArtifactID bool
	OwnerID    bool
}

type SharedArtifactOutput struct {
	response.Status `parameter:",kind=output,in=status" json:",omitempty"`
	Data            []*SharedArtifact `parameter:",kind=output,in=view" view:"forge_report_shared_artifact,batch=1000,relationalConcurrency=1" sql:"uri=sql/shared_artifact.sql"`
	Metrics         response.Metrics  `parameter:",kind=output,in=metrics"`
}

func DefineSharedArtifactComponent(ctx context.Context, srv *datly.Service, connectorRef string) error {
	normalizedConnector := strings.TrimSpace(connectorRef)
	if normalizedConnector == "" {
		normalizedConnector = "agently"
	}
	component, err := repository.NewComponent(
		contract.NewPath("GET", SharedArtifactPathURI),
		repository.WithResource(srv.Resource()),
		repository.WithContract(
			reflect.TypeOf(SharedArtifactInput{}),
			reflect.TypeOf(SharedArtifactOutput{}),
			&FS,
			view.WithConnectorRef(normalizedConnector),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to create forge reporting shared artifact component: %w", err)
	}
	if err := srv.AddComponent(ctx, component); err != nil {
		return fmt.Errorf("failed to add forge reporting shared artifact component: %w", err)
	}
	return nil
}

func (i *SharedArtifactInput) EmbedFS() *embed.FS { return &FS }

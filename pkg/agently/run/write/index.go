package write

import (
	"context"
	"reflect"

	"github.com/viant/agently-core/internal/datlycompat"
	"github.com/viant/datly"
	"github.com/viant/datly/repository"
	"github.com/viant/datly/repository/contract"
)

var PathURI = "/v1/api/agently/run"

func DefineComponent(ctx context.Context, srv *datly.Service) (*repository.Component, error) {
	return datlycompat.AddHandler(ctx, srv, contract.NewPath("PATCH", PathURI), &Handler{},
		repository.WithResource(srv.Resource()),
		repository.WithContract(
			reflect.TypeOf(&Input{}),
			reflect.TypeOf(&Output{}),
			&FS))
}

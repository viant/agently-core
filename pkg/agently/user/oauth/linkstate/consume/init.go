package consume

import (
	"context"
	"reflect"

	"github.com/viant/datly"
	"github.com/viant/datly/repository"
	"github.com/viant/datly/repository/contract"
	"github.com/viant/datly/view"
)

var PathURI = "/v1/api/agently/user/oauth/linkstate/consume"

func DefineComponent(ctx context.Context, srv *datly.Service) (*repository.Component, error) {
	return srv.AddHandler(ctx, contract.NewPath("POST", PathURI), &Handler{},
		repository.WithResource(srv.Resource()),
		repository.WithContract(
			reflect.TypeOf(&Input{}),
			reflect.TypeOf(&Output{}),
			nil,
			view.WithConnectorRef("agently")),
	)
}

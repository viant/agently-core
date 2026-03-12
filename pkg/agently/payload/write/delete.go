package write

import (
	"context"
	"fmt"
	"net/http"
	"reflect"

	"github.com/viant/agently-core/internal/datlycompat"
	"github.com/viant/datly"
	"github.com/viant/datly/repository"
	"github.com/viant/datly/repository/contract"
	"github.com/viant/xdatly/handler"
	"github.com/viant/xdatly/handler/response"
)

type DeleteInput struct {
	Rows []*MutablePayloadView `parameter:",kind=body,in=data"`
}

type DeleteOutput struct {
	response.Status `parameter:",kind=output,in=status" json:",omitempty"`
}

type DeleteHandler struct{}

func (h *DeleteHandler) Exec(ctx context.Context, sess handler.Session) (interface{}, error) {
	in := &DeleteInput{}
	if err := sess.Stater().Bind(ctx, in); err != nil {
		return nil, err
	}
	sqlxService, err := sess.Db()
	if err != nil {
		return nil, err
	}
	for _, row := range in.Rows {
		if row == nil || row.Id == "" {
			continue
		}
		if err := sqlxService.Delete("call_payload", row); err != nil {
			return &DeleteOutput{Status: response.Status{Status: "error", Message: err.Error()}}, response.NewError(http.StatusBadRequest, "bad request")
		}
	}
	return &DeleteOutput{Status: response.Status{Status: "ok"}}, nil
}

func DefineDeleteComponent(ctx context.Context, srv *datly.Service) (*repository.Component, error) {
	component, err := datlycompat.AddHandler(ctx, srv, contract.NewPath(http.MethodDelete, PathURI), &DeleteHandler{},
		repository.WithResource(srv.Resource()),
		repository.WithContract(reflect.TypeOf(&DeleteInput{}), reflect.TypeOf(&DeleteOutput{}), &FS))
	if err != nil {
		return nil, fmt.Errorf("failed to add payload delete handler: %w", err)
	}
	return component, nil
}

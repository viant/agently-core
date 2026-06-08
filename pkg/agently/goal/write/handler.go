package write

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/viant/xdatly/handler"
	"github.com/viant/xdatly/handler/response"
)

type Handler struct{}

func (h *Handler) Exec(ctx context.Context, sess handler.Session) (interface{}, error) {
	out := &Output{}
	out.Status.Status = "ok"
	err := h.exec(ctx, sess, out)
	if err != nil {
		var responseError *response.Error
		if errors.As(err, &responseError) {
			return out, err
		}
		out.setError(err)
	}
	if len(out.Violations) > 0 {
		out.setError(fmt.Errorf("failed validation"))
		return out, response.NewError(http.StatusBadRequest, "bad request")
	}
	return out, nil
}

func (h *Handler) exec(ctx context.Context, sess handler.Session, output *Output) error {
	input := &Input{}
	if err := input.Init(ctx, sess, output); err != nil {
		return err
	}
	output.Data = input.Goals
	if err := input.Validate(ctx, sess, output); err != nil || len(output.Violations) > 0 {
		return err
	}
	sqlSvc, err := sess.Db()
	if err != nil {
		return err
	}
	for _, rec := range input.Goals {
		if rec == nil {
			continue
		}
		_, ok := input.CurGoalById[rec.Id]
		if !ok {
			if err = sqlSvc.Insert("goal", rec); err != nil {
				return err
			}
		} else {
			if err = sqlSvc.Update("goal", rec); err != nil {
				return err
			}
		}
	}
	return nil
}

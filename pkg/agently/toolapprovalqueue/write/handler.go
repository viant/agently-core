package write

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"

	"github.com/viant/xdatly/handler"
	"github.com/viant/xdatly/handler/response"
)

type Handler struct{}

func (h *Handler) Exec(ctx context.Context, sess handler.Session) (interface{}, error) {
	out := &Output{}
	out.Status.Status = "ok"
	if err := h.exec(ctx, sess, out); err != nil {
		var rErr *response.Error
		if errors.As(err, &rErr) {
			return out, err
		}
		out.setError(err)
	}
	if len(out.Violations) > 0 {
		out.setError(fmt.Errorf("failed validation"))
		return out, response.NewError(http.StatusBadRequest, "bad request: "+out.Violations[0].Message)
	}
	return out, nil
}

func (h *Handler) exec(ctx context.Context, sess handler.Session, out *Output) error {
	in := &Input{}
	if err := in.Init(ctx, sess, out); err != nil {
		return err
	}
	out.Data = in.Queues
	if err := in.Validate(ctx, sess, out); err != nil || len(out.Violations) > 0 {
		return err
	}
	sql, err := sess.Db()
	if err != nil {
		return err
	}
	for _, rec := range in.Queues {
		if rec == nil || rec.Id == "" {
			continue
		}
		current, ok := in.CurByID[rec.Id]
		if !ok {
			if err = sql.Insert("tool_approval_queue", rec); err != nil {
				return err
			}
			continue
		}
		if err = sql.Update("tool_approval_queue", mergeToolApprovalQueuePatch(current, rec)); err != nil {
			return err
		}
	}
	return nil
}

func mergeToolApprovalQueuePatch(current, patch *ToolApprovalQueue) *ToolApprovalQueue {
	if patch == nil || patch.Has == nil || current == nil {
		return patch
	}
	merged := *patch
	currentValue := reflect.ValueOf(current).Elem()
	mergedValue := reflect.ValueOf(&merged).Elem()
	hasValue := reflect.ValueOf(merged.Has).Elem()
	hasType := hasValue.Type()
	for i := 0; i < hasValue.NumField(); i++ {
		if hasValue.Field(i).Bool() {
			continue
		}
		fieldName := hasType.Field(i).Name
		mergedField := mergedValue.FieldByName(fieldName)
		currentField := currentValue.FieldByName(fieldName)
		if !mergedField.IsValid() || !currentField.IsValid() || !mergedField.CanSet() {
			continue
		}
		mergedField.Set(currentField)
	}
	return &merged
}

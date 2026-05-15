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

	output := &Output{}
	output.Status.Status = "ok"
	err := h.exec(ctx, sess, output)
	if err != nil {
		var responseError *response.Error
		if errors.As(err, &responseError) {
			return output, err
		}
		output.setError(err)
	}
	if len(output.Violations) > 0 {
		output.setError(fmt.Errorf("failed validation"))
		return output, response.NewError(http.StatusBadRequest, "bad request")
	}
	return output, nil
}

func (h *Handler) exec(ctx context.Context, sess handler.Session, output *Output) error {
	input := &Input{}
	if err := input.Init(ctx, sess, output); err != nil {
		return err
	}
	output.Data = input.Conversations
	if err := input.Validate(ctx, sess, output); err != nil || len(output.Violations) > 0 {
		return err
	}
	sql, err := sess.Db()
	if err != nil {
		return err
	}
	conversations := input.Conversations

	for _, recConversation := range conversations {
		current, ok := input.CurConversationById[recConversation.Id]
		if !ok {
			if err = sql.Insert("conversation", recConversation); err != nil {
				return err
			}
		} else {
			if err = sql.Update("conversation", mergeConversationPatch(current, recConversation)); err != nil {
				return err
			}
		}
	}
	return nil
}

func mergeConversationPatch(current, patch *Conversation) *Conversation {
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

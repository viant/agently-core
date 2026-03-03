package conversation

import (
	"context"
	"reflect"
)

var inputKey = reflect.TypeOf(&ConversationRowsInput{})

func InputFromContext(ctx context.Context) *ConversationRowsInput {
	if ctx == nil {
		return nil
	}
	value := ctx.Value(inputKey)
	if value == nil {
		return nil
	}
	input, _ := value.(*ConversationRowsInput)
	return input
}

package run

import (
	"context"
	"reflect"
)

var inputKey = reflect.TypeOf(&RunRowsInput{})

func InputFromContext(ctx context.Context) *RunRowsInput {
	if ctx == nil {
		return nil
	}
	value := ctx.Value(inputKey)
	if value == nil {
		return nil
	}
	input, _ := value.(*RunRowsInput)
	return input
}

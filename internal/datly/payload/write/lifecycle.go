package write

import (
	"bytes"
	"compress/gzip"
	context "context"
	"fmt"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"strings"
	"time"
)

func (input *Input) Init(context.Context) error {
	if !input.DeleteUnreferenced {
		return nil
	}
	if len(input.Payloads) != 1 || input.Payloads[0] == nil || !input.Payloads[0].ShouldDelete || strings.TrimSpace(input.Payloads[0].Id) == "" {
		return fmt.Errorf("unreferenced payload deletion requires one delete-marked identity")
	}
	return nil
}

// Lifecycle customizes role Input.Payloads.
type Lifecycle struct{}

func LifecycleDatlyType() reflect.Type {
	return reflect.TypeOf((*Lifecycle)(nil)).Elem()
}

var (
	LifecycleHooks = new(Lifecycle)
	LifecycleDatly = LifecycleDatlyType()
)

func (hooks *Lifecycle) Init(ctx context.Context, entity *Payload, state xhandler.LifecycleContext[Payload, xhandler.NoParent, Output]) error {
	if entity == nil {
		return nil
	}
	if entity.ShouldDelete {
		return nil
	}
	if entity.Has == nil {
		entity.Has = &PayloadHas{}
	}
	if state.Previous == nil {
		now := time.Now()
		entity.SetCreatedAt(&now)
		if !entity.Has.Compression {
			entity.SetCompression("none")
		}
		if entity.Redacted == nil {
			zero := 0
			entity.SetRedacted(&zero)
		}
	}
	if entity.Has.InlineBody && entity.InlineBody != nil &&
		(!entity.Has.Compression || entity.Compression == "none") &&
		(entity.Has.Storage && entity.Storage == "inline" || !entity.Has.Storage) && len(*entity.InlineBody) > 1024 {
		var buffer bytes.Buffer
		compressor := gzip.NewWriter(&buffer)
		if _, err := compressor.Write(*entity.InlineBody); err != nil {
			return err
		}
		if err := compressor.Close(); err != nil {
			return err
		}
		body := buffer.Bytes()
		entity.SetInlineBody(&body)
		entity.SetCompression("gzip")
		entity.SetSizeBytes(len(body))
	}
	if entity.Has.Storage && entity.Storage == "object" {
		entity.SetInlineBody(nil)
	}
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *Payload, state xhandler.LifecycleContext[Payload, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *Payload, state xhandler.LifecycleContext[Payload, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *Payload, state xhandler.LifecycleContext[Payload, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}

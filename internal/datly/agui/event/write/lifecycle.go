package write

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	store "github.com/viant/agently-core/app/store/agui"
	xhandler "github.com/viant/xdatly/handler"
	"reflect"
)

type Lifecycle struct{}

func LifecycleDatlyType() reflect.Type { return reflect.TypeFor[Lifecycle]() }

var LifecycleDatly = LifecycleDatlyType()

func (*Lifecycle) Init(_ context.Context, row *Event, state xhandler.LifecycleContext[Event, xhandler.NoParent, Output]) error {
	if row == nil || state.Previous != nil || row.EventKey == nil || *row.EventKey == "" || row.RunKey == nil || *row.RunKey == "" || row.Sequence < 1 || row.Kind == nil || *row.Kind != "agui.event" || row.MimeType == nil || *row.MimeType != "application/json" || row.Storage == nil || *row.Storage != "inline" || row.Compression == nil || *row.Compression != "none" || row.SizeBytes == nil || *row.SizeBytes != len(row.EventJson) || !json.Valid(row.EventJson) {
		return store.ErrConflict
	}
	digest := sha256.Sum256(row.EventJson)
	if row.Digest == nil || *row.Digest != hex.EncodeToString(digest[:]) {
		return store.ErrConflict
	}
	return nil
}
func (*Lifecycle) Validate(context.Context, *Event, xhandler.LifecycleContext[Event, xhandler.NoParent, Output]) error {
	return nil
}
func (*Lifecycle) AfterSequence(context.Context, *Event, xhandler.LifecycleContext[Event, xhandler.NoParent, Output]) error {
	return nil
}
func (*Lifecycle) AfterQueue(context.Context, *Event, xhandler.LifecycleContext[Event, xhandler.NoParent, Output]) error {
	return nil
}
func (*Lifecycle) Finalize(context.Context, *Input, *Output, xhandler.Outcome) error { return nil }

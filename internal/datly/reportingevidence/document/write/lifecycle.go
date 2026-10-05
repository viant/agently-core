package write

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	store "github.com/viant/agently-core/app/store/reportingevidence"
	xhandler "github.com/viant/xdatly/handler"
	"reflect"
)

type Lifecycle struct{}

func LifecycleDatlyType() reflect.Type { return reflect.TypeFor[Lifecycle]() }

var LifecycleDatly = LifecycleDatlyType()

func (*Lifecycle) Init(_ context.Context, row *Document, state xhandler.LifecycleContext[Document, xhandler.NoParent, Output]) error {
	if row == nil || state.Previous != nil || row.Id == nil || *row.Id == "" || row.RunId == nil || *row.RunId == "" || row.Kind == nil || *row.Kind != "attachment" || row.Subtype == nil || (*row.Subtype != store.Admission && *row.Subtype != store.Plan) || row.SchemaRef == nil || *row.SchemaRef != store.Schema(*row.Subtype) || row.MimeType == nil || *row.MimeType != "application/json" || row.Storage == nil || *row.Storage != "inline" || row.Compression == nil || *row.Compression != "none" || row.SizeBytes == nil || *row.SizeBytes != len(row.InlineBody) || !json.Valid(row.InlineBody) {
		return fmt.Errorf("immutable evidence document is invalid")
	}
	digest := sha256.Sum256(row.InlineBody)
	if row.Digest == nil || *row.Digest != hex.EncodeToString(digest[:]) {
		return fmt.Errorf("immutable evidence document is invalid")
	}
	return nil
}
func (*Lifecycle) Validate(context.Context, *Document, xhandler.LifecycleContext[Document, xhandler.NoParent, Output]) error {
	return nil
}
func (*Lifecycle) AfterSequence(context.Context, *Document, xhandler.LifecycleContext[Document, xhandler.NoParent, Output]) error {
	return nil
}
func (*Lifecycle) AfterQueue(context.Context, *Document, xhandler.LifecycleContext[Document, xhandler.NoParent, Output]) error {
	return nil
}
func (*Lifecycle) Finalize(context.Context, *Input, *Output, xhandler.Outcome) error { return nil }

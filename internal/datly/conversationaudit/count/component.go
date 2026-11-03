// Package count defines the lightweight, trusted conversation audit count.
package count

import (
	"context"
	"embed"
	"fmt"
	"reflect"
	"time"

	projection "github.com/viant/agently-core/internal/datly/conversationaudit/read"
	"github.com/viant/xdatly"
)

//go:embed sql/*.sql
var resources embed.FS

type ReaderComponent struct {
	Contract xdatly.Component[Input, Output] `component:"reader,path=/v1/internal/agently/conversation-audit/count,method=GET,connector=agently,view=reader,internal=true" routeName:"reader" caseFormat:"lc"`
}

var _anchorReaderComponent = reflect.TypeFor[ReaderComponent]()

func (ReaderComponent) EmbedFS() *embed.FS     { return &resources }
func (ReaderComponent) EmbedNamespace() string { return "datly_conversation_audit_count" }

type Input struct {
	Allowed         bool   `parameter:"Allowed,kind=auditaccess,in=allowed,dataType=bool,required=true"`
	OwnerID         string `parameter:"OwnerID,kind=auditaccess,in=owner,dataType=string,required=true"`
	OwnerIDContains string `parameter:"OwnerIDContains,kind=auditaccess,in=ownerContains,dataType=string,required=true"`
	ConversationID  string `parameter:"ConversationID,kind=query,in=conversationId,dataType=string,required=true"`
	From            string `parameter:"From,kind=query,in=from,dataType=string,required=true"`
	To              string `parameter:"To,kind=query,in=to,dataType=string,required=true"`
}

func (i *Input) Init(context.Context) error {
	if !i.Allowed {
		return fmt.Errorf("trusted audit scope required")
	}
	if i.ConversationID == "" {
		from, fromErr := time.Parse(projection.DatabaseTimeFormat, i.From)
		to, toErr := time.Parse(projection.DatabaseTimeFormat, i.To)
		if fromErr != nil || toErr != nil || !to.After(from) || to.Sub(from) > 31*24*time.Hour {
			return fmt.Errorf("bounded conversation selection required")
		}
	}
	return nil
}

type Output struct {
	Data []*Row `parameter:"Data,kind=output,in=view,dataType=[]*Row" view:"reader,type=Row,table=turn" json:"data" sql:"uri=datly_conversation_audit_count:sql/reader.sql"`
}

type Row struct {
	TotalCount int64 `sqlx:"total_count" json:"totalCount"`
}

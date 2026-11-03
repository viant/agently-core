// Package read defines an internal, read-only conversation audit projection.
package read

import (
	"context"
	"embed"
	"fmt"
	"reflect"
	"time"

	"github.com/viant/xdatly"
)

//go:embed sql/*.sql
var resources embed.FS

type ReaderComponent struct {
	Contract xdatly.Component[Input, Output] `component:"reader,path=/v1/internal/agently/conversation-audit,method=GET,connector=agently,view=reader,internal=true" routeName:"reader" caseFormat:"lc"`
}

var _anchorReaderComponent = reflect.TypeFor[ReaderComponent]()

func (ReaderComponent) EmbedFS() *embed.FS     { return &resources }
func (ReaderComponent) EmbedNamespace() string { return "datly_conversation_audit" }

type Input struct {
	Allowed         bool      `parameter:"Allowed,kind=auditaccess,in=allowed,dataType=bool,required=true"`
	OwnerID         string    `parameter:"OwnerID,kind=auditaccess,in=owner,dataType=string,required=true"`
	OwnerIDContains string    `parameter:"OwnerIDContains,kind=auditaccess,in=ownerContains,dataType=string,required=true"`
	ConversationID  string    `parameter:"ConversationID,kind=query,in=conversationId,dataType=string,required=true"`
	TurnID          string    `parameter:"TurnID,kind=query,in=turnId,dataType=string,required=true"`
	TurnIDs         []string  `parameter:"TurnIDs,kind=query,in=turnIds,dataType=[]string,required=false" predicate:"in,t,id"`
	From            string    `parameter:"From,kind=query,in=from,dataType=string,required=true"`
	To              string    `parameter:"To,kind=query,in=to,dataType=string,required=true"`
	CursorTime      time.Time `parameter:"CursorTime,kind=query,in=cursorTime,dataType=time.Time,required=true"`
	CursorID        string    `parameter:"CursorID,kind=query,in=cursorId,dataType=string,required=true"`
	Detail          bool      `parameter:"Detail,kind=query,in=detail,dataType=bool,required=true"`
	Limit           int       `parameter:"Limit,kind=query,in=limit,dataType=int,required=true" querySelector:"view=reader,property=limit"`
	Has             *InputHas `setMarker:"true" json:"-" sqlx:"-"`
}

type InputHas struct {
	TurnIDs bool
}

func (i *Input) Init(context.Context) error {
	if !i.Allowed {
		return fmt.Errorf("trusted audit scope required")
	}
	if i.Limit < 1 || i.Limit > 201 {
		return fmt.Errorf("invalid audit limit")
	}
	if len(i.TurnIDs) > 200 || (i.Detail && i.TurnID == "" && len(i.TurnIDs) == 0) {
		return fmt.Errorf("bounded audit detail selection required")
	}
	for _, id := range i.TurnIDs {
		if id == "" || len(id) > 256 {
			return fmt.Errorf("invalid audit turn ID")
		}
	}
	if i.ConversationID == "" {
		from, fromErr := time.Parse(DatabaseTimeFormat, i.From)
		to, toErr := time.Parse(DatabaseTimeFormat, i.To)
		if fromErr != nil || toErr != nil || !to.After(from) || to.Sub(from) > 31*24*time.Hour {
			return fmt.Errorf("bounded conversation selection required")
		}
	}
	return nil
}

// Use UTC SQL timestamps without driver-specific timezone suffixes. This also
// compares correctly with SQLite's CURRENT_TIMESTAMP values at exact bounds.
const DatabaseTimeFormat = "2006-01-02 15:04:05.999999999"

type Output struct {
	Data []*Row `parameter:"Data,kind=output,in=view,dataType=[]*Row" view:"reader,type=Row,table=turn,selectorLimit=true" json:"data" sql:"uri=datly_conversation_audit:sql/reader.sql"`
}

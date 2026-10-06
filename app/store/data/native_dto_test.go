package data

import (
	"bytes"
	"fmt"
	convwrite "github.com/viant/agently-core/internal/datly/conversation/write"
	modelwrite "github.com/viant/agently-core/internal/datly/modelcall/write"
	payloadwrite "github.com/viant/agently-core/internal/datly/payload/write"
	turnwrite "github.com/viant/agently-core/internal/datly/turn/write"
	queuewrite "github.com/viant/agently-core/internal/datly/turnqueue/write"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	modelcallmodel "github.com/viant/agently-core/model/modelcall"
	payloadmodel "github.com/viant/agently-core/model/payload"
	turnmodel "github.com/viant/agently-core/model/turn"
	turnqueuemodel "github.com/viant/agently-core/model/turnqueue"
	"reflect"
	"strings"
	"testing"

	msgwrite "github.com/viant/agently-core/internal/datly/message/write"
	payloadread "github.com/viant/agently-core/internal/datly/payload/reference"
	toolwrite "github.com/viant/agently-core/internal/datly/toolcall/write"
	messagemodel "github.com/viant/agently-core/model/message"
	toolcallmodel "github.com/viant/agently-core/model/toolcall"
)

func TestDataDTO_SparseNullAndZeroPresence(t *testing.T) {
	row := &messagemodel.MutableMessageView{}
	row.SetId("m")
	row.SetConversationID("c")
	row.Content = nil
	row.Has.Content = true
	row.SetInterim(0)
	native, err := mapDataDTO[msgwrite.Message](row)
	if err != nil {
		t.Fatal(err)
	}
	if native.Has == nil || !native.Has.ConversationId || !native.Has.Content || !native.Has.Interim || native.Has.Role || native.Has.CreatedAt {
		t.Fatalf("presence changed: %#v", native.Has)
	}
	if native.ConversationId != "c" || native.Content != nil || native.Interim == nil || *native.Interim != 0 {
		t.Fatalf("null/zero mutation changed: %#v", native)
	}
	back, err := mapDataDTO[messagemodel.MutableMessageView](native)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(row, back) {
		t.Fatalf("sparse mutation changed after round trip: %#v %#v", row, back)
	}
}
func TestDataDTO_PayloadBytesRemainText(t *testing.T) {
	raw := []byte("{\"content\":\"héllo\"}\x00")
	source := &payloadread.PayloadView{Id: "p", InlineBody: &raw}
	result, err := mapDataDTO[payloadmodel.PayloadRowsView](source)
	if err != nil {
		t.Fatal(err)
	}
	if result.InlineBody == nil || !bytes.Equal([]byte(*result.InlineBody), raw) {
		t.Fatalf("payload reader changed raw bytes: %#v", result.InlineBody)
	}
}
func TestDataDTO_ToolCallAcronymPresence(t *testing.T) {
	row := &toolcallmodel.MutableToolCallView{}
	row.SetMessageID("m")
	trace := "trace"
	row.TraceID = &trace
	row.Has.TraceID = true
	row.SetRunID("run")
	zero := 0
	row.LatencyMS = &zero
	row.Has.LatencyMS = true
	mapped, err := mapDataDTO[toolwrite.ToolCall](row)
	if err != nil {
		t.Fatal(err)
	}
	if mapped.MessageId != "m" || mapped.TraceId == nil || *mapped.TraceId != "trace" || mapped.RunId == nil || *mapped.RunId != "run" || mapped.Has == nil || !mapped.Has.MessageId || !mapped.Has.TraceId || !mapped.Has.RunId || !mapped.Has.LatencyMs {
		t.Fatalf("tool call acronym mapping changed: %#v", mapped)
	}
}

func TestDataDTO_AllPublicMutationPresenceFields(t *testing.T) {
	cases := []struct {
		name           string
		source, target any
	}{
		{"conversation", &conversationmodel.MutableConversationView{}, &convwrite.MutableConversationView{}},
		{"message", &messagemodel.Message{}, &msgwrite.Message{}},
		{"turn", &turnmodel.Turn{}, &turnwrite.Turn{}},
		{"modelcall", &modelcallmodel.ModelCall{}, &modelwrite.ModelCall{}},
		{"toolcall", &toolcallmodel.ToolCall{}, &toolwrite.ToolCall{}},
		{"payload", &payloadmodel.Payload{}, &payloadwrite.Payload{}},
		{"queue", &turnqueuemodel.TurnQueue{}, &queuewrite.TurnQueue{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := reflect.ValueOf(tc.source).Elem()
			has := source.FieldByName("Has")
			has.Set(reflect.New(has.Type().Elem()))
			for i := 0; i < has.Elem().NumField(); i++ {
				has.Elem().Field(i).SetBool(true)
			}
			if err := copyDataValue(reflect.ValueOf(tc.target).Elem(), reflect.ValueOf(tc.source)); err != nil {
				t.Fatal(err)
			}
			mapped := reflect.ValueOf(tc.target).Elem().FieldByName("Has").Elem()
			for i := 0; i < has.Elem().NumField(); i++ {
				name := has.Elem().Type().Field(i).Name
				found := false
				for j := 0; j < mapped.NumField(); j++ {
					if strings.EqualFold(name, mapped.Type().Field(j).Name) {
						found = mapped.Field(j).Bool()
						break
					}
				}
				if !found && !(tc.name == "toolcall" && name == "ResponseOverflow") {
					t.Errorf("public mutation presence field %s disappeared", name)
				}
			}
		})
	}
}

func TestDataDTO_ApplyCanonicalResultPreservesLogicalFields(t *testing.T) {
	public := &toolcallmodel.ToolCall{}
	public.SetMessageID("m")
	public.SetResponseOverflow(true)
	canonical := &toolwrite.ToolCall{}
	canonical.SetMessageId("m")
	canonical.SetAttempt(1)
	result, err := mapDataDTO[toolcallmodel.ToolCall](public)
	if err != nil {
		t.Fatal(err)
	}
	if err = applyDataMutationResult(reflect.ValueOf(result).Elem(), reflect.ValueOf(canonical)); err != nil {
		t.Fatal(err)
	}
	if !result.ResponseOverflow || !result.Has.ResponseOverflow || !result.Has.Attempt || result.Attempt != 1 {
		t.Fatalf("logical field or successful defaults lost: %#v %#v", result, result.Has)
	}
	if public.Has.Attempt || public.Attempt != 0 {
		t.Fatal("mapping mutated the caller before publication")
	}
}

func TestDataDTO_ApplyCanonicalResultPreservesResponseOverflow(t *testing.T) {
	for _, publicValue := range []bool{false, true} {
		for _, publicPresence := range []string{"nil", "absent", "present"} {
			for _, canonicalValue := range []bool{false, true} {
				for _, canonicalPresence := range []string{"nil", "absent", "present"} {
					name := fmt.Sprintf("public=%t/%s/canonical=%t/%s", publicValue, publicPresence, canonicalValue, canonicalPresence)
					t.Run(name, func(t *testing.T) {
						cost, zero := 42.5, 0
						public := &toolcallmodel.ToolCall{MessageID: "m", ResponseOverflow: publicValue, Cost: &cost}
						if publicPresence != "nil" {
							public.Has = &toolcallmodel.ToolCallHas{MessageID: true, ResponseOverflow: publicPresence == "present"}
						}
						canonical := &toolwrite.ToolCall{MessageId: "m", ResponseOverflow: canonicalValue, Attempt: 1, Status: "completed", LatencyMs: &zero}
						if canonicalPresence != "nil" {
							canonical.Has = &toolwrite.ToolCallHas{MessageId: true, ResponseOverflow: canonicalPresence == "present", Attempt: true, Status: true, Cost: true, LatencyMs: true}
						}
						publicBefore, err := mapDataDTO[toolcallmodel.ToolCall](public)
						if err != nil {
							t.Fatal(err)
						}
						canonicalBefore, err := mapDataDTO[toolwrite.ToolCall](canonical)
						if err != nil {
							t.Fatal(err)
						}
						result, err := mapDataDTO[toolcallmodel.ToolCall](public)
						if err != nil {
							t.Fatal(err)
						}
						if err = applyDataMutationResult(reflect.ValueOf(result).Elem(), reflect.ValueOf(canonical)); err != nil {
							t.Fatal(err)
						}
						wantPresence := publicPresence == "present"
						gotPresence := result.Has != nil && result.Has.ResponseOverflow
						if result.ResponseOverflow != publicValue || gotPresence != wantPresence {
							t.Fatalf("logical value/presence=%t/%t, want %t/%t", result.ResponseOverflow, gotPresence, publicValue, wantPresence)
						}
						if result.Attempt != 1 || result.Status != "completed" || result.Cost != nil || result.LatencyMS == nil || *result.LatencyMS != 0 {
							t.Fatalf("stored defaults, null or zero values lost: %#v", result)
						}
						if canonical.Has != nil && (result.Has == nil || !result.Has.Attempt || !result.Has.Status || !result.Has.Cost || !result.Has.LatencyMS) {
							t.Fatalf("stored field presence lost: %#v", result.Has)
						}
						if canonical.Has == nil && !wantPresence && result.Has != nil {
							t.Fatal("allocated presence for an absent logical field")
						}
						if result.Has != nil && result.Has == public.Has {
							t.Fatal("result aliases caller presence")
						}
						if !reflect.DeepEqual(public, publicBefore) || !reflect.DeepEqual(canonical, canonicalBefore) {
							t.Fatal("mapping changed the caller or canonical result")
						}
					})
				}
			}
		}
	}
}

func TestDataDTO_ApplyCanonicalResultRejectsNilWithoutMutation(t *testing.T) {
	public := &toolcallmodel.ToolCall{}
	public.SetMessageID("m")
	public.SetResponseOverflow(true)
	before, err := mapDataDTO[toolcallmodel.ToolCall](public)
	if err != nil {
		t.Fatal(err)
	}
	var canonical *toolwrite.ToolCall
	err = applyDataMutationResult(reflect.ValueOf(public).Elem(), reflect.ValueOf(canonical))
	if err == nil || err.Error() != "nil mutation result" {
		t.Fatalf("nil mutation error changed: %v", err)
	}
	if !reflect.DeepEqual(public, before) {
		t.Fatal("nil mutation changed the caller")
	}
}

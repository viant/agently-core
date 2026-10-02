package data

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	convwrite "github.com/viant/agently-core/internal/datly/conversation/write"
	toolwrite "github.com/viant/agently-core/internal/datly/toolcall/write"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	toolcallmodel "github.com/viant/agently-core/model/toolcall"
	dexec "github.com/viant/datly/exec"
)

type pointerResultInvoker struct {
	result func(*convwrite.Input) *convwrite.Output
}

func (p pointerResultInvoker) InvokeComponent(_ context.Context, request dexec.ComponentRequest) (any, error) {
	return p.result(request.Input.(*convwrite.Input)), nil
}
func TestNativeWriterRejectsForeignOrOmittedOutputWithoutPublishing(t *testing.T) {
	for _, mode := range []string{"foreign", "omitted"} {
		t.Run(mode, func(t *testing.T) {
			original := &conversationmodel.MutableConversationView{}
			original.SetId("owned")
			before := *original
			service := &datlyService{native: pointerResultInvoker{result: func(input *convwrite.Input) *convwrite.Output {
				if mode == "omitted" {
					return &convwrite.Output{}
				}
				other := &convwrite.MutableConversationView{}
				other.SetId("owned")
				return &convwrite.Output{Data: []*convwrite.MutableConversationView{other}}
			}}}
			_, err := nativePatchData(context.Background(), service, "conversation", []*conversationmodel.MutableConversationView{original})
			if err == nil || (!strings.Contains(err.Error(), "foreign") && !strings.Contains(err.Error(), "omitted")) {
				t.Fatalf("invalid result accepted: %v", err)
			}
			if original.CreatedAt != before.CreatedAt || original.LastActivity != before.LastActivity {
				t.Fatal("failed result published defaults")
			}
		})
	}
}
func TestNativeWriterMatchesReorderedPointerOccurrences(t *testing.T) {
	first, second := &conversationmodel.MutableConversationView{}, &conversationmodel.MutableConversationView{}
	first.SetId("same")
	second.SetId("same")
	service := &datlyService{native: pointerResultInvoker{result: func(input *convwrite.Input) *convwrite.Output {
		a, b := "first", "second"
		input.Conversations[0].SetTitle(&a)
		input.Conversations[1].SetTitle(&b)
		return &convwrite.Output{Data: []*convwrite.MutableConversationView{input.Conversations[1], input.Conversations[0]}}
	}}}
	out, err := nativePatchData(context.Background(), service, "conversation", []*conversationmodel.MutableConversationView{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0] != second || out[1] != first || *first.Title != "first" || *second.Title != "second" {
		t.Fatal("reordered equal identities lost pointer occurrence correspondence")
	}
}

type toolPointerResultInvoker struct {
	result func(*toolwrite.Input) (*toolwrite.Output, error)
}

func (p toolPointerResultInvoker) InvokeComponent(_ context.Context, request dexec.ComponentRequest) (any, error) {
	return p.result(request.Input.(*toolwrite.Input))
}

func TestNativeWriterToolCallPreservesLogicalFieldsAndPublication(t *testing.T) {
	for _, mode := range []string{"reordered", "foreign", "omitted", "writer_error"} {
		t.Run(mode, func(t *testing.T) {
			first, second := &toolcallmodel.ToolCall{}, &toolcallmodel.ToolCall{}
			first.SetMessageID("same")
			first.SetResponseOverflow(true)
			second.SetMessageID("same")
			originals := []*toolcallmodel.ToolCall{first, second}
			before, err := mapDataDTOs[toolcallmodel.ToolCall](originals)
			if err != nil {
				t.Fatal(err)
			}
			originalHas := []*toolcallmodel.ToolCallHas{first.Has, second.Has}
			var canonicalBefore []*toolwrite.ToolCall
			var prepared []*toolwrite.ToolCall
			service := &datlyService{native: toolPointerResultInvoker{result: func(input *toolwrite.Input) (*toolwrite.Output, error) {
				prepared = input.ToolCalls
				for i, row := range prepared {
					row.SetAttempt(i + 1)
					row.SetStatus("completed")
					row.SetResponseOverflow(i == 1)
					row.Has.ResponseOverflow = i == 1
				}
				var err error
				canonicalBefore, err = mapDataDTOs[toolwrite.ToolCall](prepared)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(originals, before) {
					t.Fatal("preparing canonical rows mutated the caller")
				}
				switch mode {
				case "foreign":
					return &toolwrite.Output{Data: []*toolwrite.ToolCall{prepared[0], {MessageId: "same"}}}, nil
				case "omitted":
					return &toolwrite.Output{Data: prepared[:1]}, nil
				case "writer_error":
					return nil, fmt.Errorf("writer failed")
				default:
					return &toolwrite.Output{Data: []*toolwrite.ToolCall{prepared[1], prepared[0]}}, nil
				}
			}}}
			out, err := nativePatchData(context.Background(), service, "toolcall", originals)
			if mode != "reordered" {
				wantError := mode
				if mode == "writer_error" {
					wantError = "writer failed"
				}
				if err == nil || !strings.Contains(err.Error(), wantError) {
					t.Fatalf("invalid result error=%v, want %s", err, wantError)
				}
				if out != nil || !reflect.DeepEqual(originals, before) || first.Has != originalHas[0] || second.Has != originalHas[1] {
					t.Fatal("failed batch published partial changes")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if len(out) != 2 || out[0] != second || out[1] != first {
					t.Fatal("reordered equal identities lost caller pointer correspondence")
				}
				for i, row := range originals {
					if row.ResponseOverflow != before[i].ResponseOverflow || row.Has == nil || row.Has.ResponseOverflow != before[i].Has.ResponseOverflow {
						t.Fatalf("caller %d lost logical value/presence: %#v %#v", i, row, row.Has)
					}
					if row.Attempt != i+1 || !row.Has.Attempt || row.Status != "completed" || !row.Has.Status {
						t.Fatalf("caller %d lost successful writer defaults: %#v %#v", i, row, row.Has)
					}
					if row.Has == originalHas[i] {
						t.Fatal("successful publication reused caller presence")
					}
					if *originalHas[i] != *before[i].Has {
						t.Fatal("successful publication changed previous caller presence")
					}
				}
			}
			if !reflect.DeepEqual(prepared, canonicalBefore) {
				t.Fatal("mapping changed canonical writer rows")
			}
		})
	}
}

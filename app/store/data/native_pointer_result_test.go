package data

import (
	"context"
	"strings"
	"testing"

	convwrite "github.com/viant/agently-core/internal/datly/conversation/write"
	conversationmodel "github.com/viant/agently-core/model/conversation"
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

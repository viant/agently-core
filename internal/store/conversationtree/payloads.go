package conversationtree

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	fileread "github.com/viant/agently-core/internal/datly/generatedfile/read"
	msgread "github.com/viant/agently-core/internal/datly/message/read"
	modelread "github.com/viant/agently-core/internal/datly/modelcall/read"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	toolread "github.com/viant/agently-core/internal/datly/toolcall/read"
	conversation "github.com/viant/agently-core/internal/store/conversation"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/xdatly/state"
)

var generatedFileReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[fileread.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v2/api/agently/generated-file"},
}

// CollectPayloadIDs snapshots payload candidates before graph rows are
// deleted. The later guarded writer retains IDs still referenced outside it.
func (d *Discoverer) CollectPayloadIDs(ctx context.Context, graph *Graph) ([]string, error) {
	if d == nil || d.Invoker == nil || d.OwnerID == nil {
		return nil, fmt.Errorf("conversation graph reader is not configured")
	}
	if err := d.authorize(ctx, graph); err != nil {
		return nil, err
	}
	if len(graph.Nodes) == 0 {
		return nil, nil
	}
	conversationIDs := sortedMapKeys(graph.Nodes)
	payloadIDs, messageIDs := []string{}, []string{}
	query := &msgread.MessagesInput{}
	query.SetConversationIds(conversationIDs)
	messages, err := (&conversation.MessageStore{Invoker: d.Invoker, OwnerID: d.OwnerID}).ListRows(ctx, query, deleteSelectors("id", "attachment_payload_id", "elicitation_payload_id"))
	if err != nil {
		return nil, err
	}
	for _, row := range messages {
		if row == nil {
			continue
		}
		messageIDs = append(messageIDs, row.Id)
		payloadIDs = appendPayloadID(payloadIDs, row.AttachmentPayloadId, row.ElicitationPayloadId)
	}
	if messageIDs = normalizeIDs(messageIDs); len(messageIDs) > 0 {
		owner := strings.TrimSpace(d.OwnerID(ctx))
		modelQuery := &modelread.ModelCallsInput{}
		modelQuery.SetMessageIds(messageIDs)
		modelValue, err := d.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: modelReaderTarget, Input: modelQuery,
			Providers: payloadCallProviders("modelcallaccess", owner, []string{"message_id", "request_payload_id", "response_payload_id", "provider_request_payload_id", "provider_response_payload_id", "stream_payload_id"})})
		if err != nil {
			return nil, err
		}
		modelOutput, ok := modelValue.(*modelread.ModelCallsOutput)
		if !ok || modelOutput == nil {
			return nil, fmt.Errorf("model call reader returned %T", modelValue)
		}
		for _, row := range modelOutput.Data {
			if row != nil {
				payloadIDs = appendPayloadID(payloadIDs, row.RequestPayloadId, row.ResponsePayloadId,
					row.ProviderRequestPayloadId, row.ProviderResponsePayloadId, row.StreamPayloadId)
			}
		}
		toolQuery := &toolread.ToolCallsInput{}
		toolQuery.SetMessageIds(messageIDs)
		toolValue, err := d.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: toolReaderTarget, Input: toolQuery,
			Providers: payloadCallProviders("toolcallaccess", owner, []string{"message_id", "request_payload_id", "response_payload_id"})})
		if err != nil {
			return nil, err
		}
		toolOutput, ok := toolValue.(*toolread.ToolCallsOutput)
		if !ok || toolOutput == nil {
			return nil, fmt.Errorf("tool call reader returned %T", toolValue)
		}
		for _, row := range toolOutput.Data {
			if row != nil {
				payloadIDs = appendPayloadID(payloadIDs, row.RequestPayloadId, row.ResponsePayloadId)
			}
		}
	}
	fileQuery := &fileread.Input{}
	fileQuery.SetConversationIDs(conversationIDs)
	fileValue, err := d.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: generatedFileReaderTarget, Input: fileQuery, Providers: planProviders("generatedfileaccess", d.OwnerID(ctx), "id", "payload_id")})
	if err != nil {
		return nil, err
	}
	fileOutput, ok := fileValue.(*fileread.Output)
	if !ok || fileOutput == nil {
		return nil, fmt.Errorf("generated file reader returned %T", fileValue)
	}
	for _, row := range fileOutput.Data {
		if row != nil {
			payloadIDs = appendPayloadID(payloadIDs, row.PayloadId)
		}
	}
	return normalizeIDs(payloadIDs), nil
}

func payloadCallProviders(kind, owner string, fields []string) []locator.Provider {
	return []locator.Provider{
		provider.Named(kind, func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			switch name {
			case "internal":
				return true, true, nil
			case "mode":
				return "rows", true, nil
			}
			return nil, false, nil
		}),
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }),
		queryselectors.Provider(state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: fields}}}),
	}
}

func appendPayloadID(target []string, pointers ...*string) []string {
	for _, value := range pointers {
		if value != nil {
			target = append(target, *value)
		}
	}
	return target
}

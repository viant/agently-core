package write

import (
	context "context"
	"fmt"

	"github.com/viant/agently-core/internal/datly/invariant"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"strings"
)

// Lifecycle customizes role Input.ToolCalls.
type Lifecycle struct {
	Input *Input `bind:"kind=input"`
}

func LifecycleDatlyType() reflect.Type {
	return reflect.TypeOf((*Lifecycle)(nil)).Elem()
}

var (
	LifecycleHooks = new(Lifecycle)
	LifecycleDatly = LifecycleDatlyType()
)

func (hooks *Lifecycle) Init(ctx context.Context, entity *ToolCall, state xhandler.LifecycleContext[ToolCall, xhandler.NoParent, Output]) error {
	if hooks.Input != nil && hooks.Input.OrphanDetach {

		return invariant.ValidateOrphanDetach(entity, state.Previous, hooks.Input.OrphanColumn, []string{"turn_id", "request_payload_id", "response_payload_id", "run_id"}, "MessageId")
	}

	if entity == nil || entity.ShouldDelete {
		return nil
	}
	if entity.Has != nil && entity.Has.ErrorMessage && entity.ErrorMessage != nil {
		sanitized := SanitizeErrorMessage(*entity.ErrorMessage)
		entity.SetErrorMessage(&sanitized)
	}
	if state.Previous == nil && (entity.Has == nil || !entity.Has.Attempt) {
		entity.SetAttempt(1)
	}
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *ToolCall, state xhandler.LifecycleContext[ToolCall, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *ToolCall, state xhandler.LifecycleContext[ToolCall, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *ToolCall, state xhandler.LifecycleContext[ToolCall, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}

func (input *Input) Init(context.Context) error {
	if input.ToolCalls == nil {
		return nil
	}
	rows := make([]*ToolCall, 0, len(input.ToolCalls))
	for _, row := range input.ToolCalls {
		if row != nil && row.ShouldDelete && row.MessageId == "" {
			continue
		}
		rows = append(rows, row)
	}
	input.ToolCalls = rows
	return nil
}

const MaxErrorMessageBytes = 60000

const replacementRune = "\uFFFD"

// SanitizeErrorMessage returns a DB-safe, UTF-8-valid tool error summary.
func SanitizeErrorMessage(value string) string {
	return summarizeErrorMessage(value, MaxErrorMessageBytes)
}

func summarizeErrorMessage(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	normalized := strings.ToValidUTF8(value, replacementRune)
	if len(normalized) <= limit {
		return normalized
	}
	marker := truncationMarker(len(value), len(normalized))
	var head, tail string
	for i := 0; i < 4; i++ {
		available := limit - len(marker)
		if available <= 0 {
			return trimUTF8Head(marker, limit)
		}
		headBudget := available / 2
		tailBudget := available - headBudget
		head = trimUTF8Head(normalized, headBudget)
		tail = trimUTF8Tail(normalized, tailBudget)
		nextMarker := truncationMarker(len(value), len(normalized)-len(head)-len(tail))
		if nextMarker == marker {
			break
		}
		marker = nextMarker
	}
	result := head + marker + tail
	for len(result) > limit {
		available := limit - len(marker)
		if available <= 0 {
			return trimUTF8Head(marker, limit)
		}
		headBudget := available / 2
		tailBudget := available - headBudget
		head = trimUTF8Head(head, headBudget)
		tail = trimUTF8Tail(tail, tailBudget)
		result = head + marker + tail
	}
	return result
}

func truncationMarker(originalBytes, omittedBytes int) string {
	if omittedBytes < 0 {
		omittedBytes = 0
	}
	return fmt.Sprintf("\n\n[tool_call.error_message truncated: original=%d bytes omitted=%d bytes]\n\n", originalBytes, omittedBytes)
}

func trimUTF8Head(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	end := 0
	for index := range value {
		if index > maxBytes {
			break
		}
		end = index
	}
	if end == 0 && maxBytes > 0 {
		return ""
	}
	return value[:end]
}

func trimUTF8Tail(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	target := len(value) - maxBytes
	for index := range value {
		if index >= target {
			return value[index:]
		}
	}
	return ""
}

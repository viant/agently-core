package write

import (
	context "context"
	"fmt"

	"github.com/viant/agently-core/internal/datly/invariant"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"time"
	"unicode/utf8"
)

// Lifecycle customizes role Input.Messages.
type Lifecycle struct {
	Input     *Input `bind:"kind=input"`
	createdAt *time.Time
}

func LifecycleDatlyType() reflect.Type {
	return reflect.TypeOf((*Lifecycle)(nil)).Elem()
}

var (
	LifecycleHooks = new(Lifecycle)
	LifecycleDatly = LifecycleDatlyType()
)

func (hooks *Lifecycle) Init(ctx context.Context, entity *Message, state xhandler.LifecycleContext[Message, xhandler.NoParent, Output]) error {
	if hooks.Input != nil && hooks.Input.OrphanDetach {
		if hooks.Input.DetachLinks || hooks.Input.TerminalCleanup {
			return fmt.Errorf("message mutation modes are mutually exclusive")
		}
		return invariant.ValidateOrphanDetach(entity, state.Previous, hooks.Input.OrphanColumn, []string{"turn_id", "parent_message_id", "superseded_by", "linked_conversation_id", "attachment_payload_id", "elicitation_payload_id"}, "Id")
	}

	if entity == nil {
		return nil
	}
	if hooks.Input != nil {
		if hooks.Input.DetachLinks && hooks.Input.TerminalCleanup {
			return fmt.Errorf("message mutation modes are mutually exclusive")
		}
		if hooks.Input.DetachLinks {
			return validateMessageLinkDetach(entity, state.Previous, hooks.Input.DetachMessageIDs)
		}
		if hooks.Input.TerminalCleanup {
			return validateTerminalMessageCleanup(entity, state.Previous)
		}
	}
	if entity.ShouldDelete {
		return nil
	}
	if entity.Has == nil {
		entity.Has = &MessageHas{}
	}
	if previous := state.Previous; previous != nil {
		if !entity.Has.ConversationId && previous.ConversationId != "" {
			entity.SetConversationId(previous.ConversationId)
		}
		if !entity.Has.TurnId && nonempty(previous.TurnId) {
			entity.SetTurnId(previous.TurnId)
		}
		if !entity.Has.ParentMessageId && nonempty(previous.ParentMessageId) {
			entity.SetParentMessageId(previous.ParentMessageId)
		}
		if !entity.Has.Role && previous.Role != "" {
			entity.SetRole(previous.Role)
		}
		if !entity.Has.Type && previous.Type != "" {
			entity.SetType(previous.Type)
		}
		if !entity.Has.Mode && nonempty(previous.Mode) {
			entity.SetMode(previous.Mode)
		}
		if !entity.Has.Iteration && previous.Iteration != nil {
			entity.SetIteration(previous.Iteration)
		}
		if !entity.Has.Phase && nonempty(previous.Phase) {
			entity.SetPhase(previous.Phase)
		}
		now := time.Now().UTC()
		entity.SetUpdatedAt(&now)
	} else {
		if hooks.createdAt == nil {
			now := time.Now()
			hooks.createdAt = &now
		}
		entity.SetCreatedAt(hooks.createdAt)
		if entity.Interim == nil {
			zero := 0
			entity.SetInterim(&zero)
		}
	}
	if value, truncated := truncateMessageContent(entity.Content); truncated {
		entity.SetContent(value)
	}
	if value, truncated := truncateMessageContent(entity.RawContent); truncated {
		entity.SetRawContent(value)
	}
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *Message, state xhandler.LifecycleContext[Message, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *Message, state xhandler.LifecycleContext[Message, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *Message, state xhandler.LifecycleContext[Message, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}

func nonempty(value *string) bool { return value != nil && *value != "" }

// MaxContentBytes preserves the legacy MEDIUMTEXT limit.
const MaxContentBytes = 16777215

func truncateMessageContent(value *string) (*string, bool) {
	if value == nil || len(*value) <= MaxContentBytes {
		return value, false
	}
	result := (*value)[:MaxContentBytes]
	for len(result) > 0 && !utf8.ValidString(result) {
		result = result[:len(result)-1]
	}
	return &result, true
}

func (input *Input) Init(context.Context) error {
	if input.Messages == nil {
		return nil
	}
	rows := make([]*Message, 0, len(input.Messages))
	for _, row := range input.Messages {
		if row != nil && row.ShouldDelete && row.Id == "" {
			continue
		}
		rows = append(rows, row)
	}
	input.Messages = rows
	return nil
}

// validateMessageLinkDetach allows the tree's trusted snapshot to clear only
// links into its message set. It preserves timestamps and every other column.
func validateMessageLinkDetach(entity, previous *Message, targets []string) error {
	if previous == nil || entity.Has == nil {
		return fmt.Errorf("message link detach requires an existing sparse row")
	}
	has := entity.Has
	if !has.ParentMessageId && !has.SupersededBy {
		return fmt.Errorf("message link detach requires a link field")
	}
	if has.ShouldDelete || has.Archived || has.ConversationId || has.TurnId || has.Sequence || has.CreatedAt || has.UpdatedAt || has.CreatedByUserId || has.Mode || has.Role || has.Status || has.Type || has.Content || has.RawContent || has.Summary || has.ContextSummary || has.EmbeddingIndex || has.Tags || has.Interim || has.ElicitationId || has.LinkedConversationId || has.ToolName || has.Narration || has.Iteration || has.Phase || has.AttachmentPayloadId || has.ElicitationPayloadId {
		return fmt.Errorf("message link detach cannot change other fields")
	}
	contains := func(value *string) bool {
		if value == nil {
			return false
		}
		for _, id := range targets {
			if id == *value {
				return true
			}
		}
		return false
	}
	if has.ParentMessageId && (entity.ParentMessageId != nil || !contains(previous.ParentMessageId)) {
		return fmt.Errorf("parent message detach is outside the trusted graph")
	}
	if has.SupersededBy && (entity.SupersededBy != nil || !contains(previous.SupersededBy)) {
		return fmt.Errorf("superseded message detach is outside the trusted graph")
	}
	return nil
}

// validateTerminalMessageCleanup preserves the captured repair timestamp and
// changes only the failed status. Explicit zero time retains legacy meaning.
func validateTerminalMessageCleanup(entity, previous *Message) error {
	if previous == nil || entity.Has == nil {
		return fmt.Errorf("terminal message cleanup requires an existing sparse row")
	}
	has := entity.Has
	if !has.Status || entity.Status == nil || *entity.Status != "failed" || !has.UpdatedAt || entity.UpdatedAt == nil {
		return fmt.Errorf("terminal message cleanup requires failed status and explicit timestamp")
	}
	if has.ShouldDelete || has.Archived || has.ConversationId || has.TurnId || has.Sequence || has.CreatedAt || has.CreatedByUserId || has.Mode || has.Role || has.Type || has.Content || has.RawContent || has.Summary || has.ContextSummary || has.EmbeddingIndex || has.Tags || has.Interim || has.ElicitationId || has.ParentMessageId || has.SupersededBy || has.LinkedConversationId || has.ToolName || has.Narration || has.Iteration || has.Phase || has.AttachmentPayloadId || has.ElicitationPayloadId {
		return fmt.Errorf("terminal message cleanup cannot change other fields")
	}
	return nil
}

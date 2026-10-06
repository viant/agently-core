package sdk

import (
	"context"

	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/aguistate"
)

func seedAGUIUserIdentity(user aguistate.Object, record *aguistore.Run) {
	metadata := aguiRecoveryObject(user["metadata"])
	if metadata == nil {
		metadata = aguistate.Object{}
	}
	namespace := aguiRecoveryObject(metadata["agently"])
	if namespace == nil {
		namespace = aguistate.Object{}
	}
	namespace["presentation"] = map[string]any{"version": "1", "conversationId": record.ConversationID, "nativeTurnId": record.TurnID, "protocolRunId": record.RunID, "clientMessageId": record.ClientMessageID, "clientRequestId": record.ClientMessageID}
	metadata["agently"] = namespace
	user["metadata"] = metadata
}
func publishAGUIUserIdentity(writer *aguiJournalWriter, translator *agui.Translator, record *aguistore.Run, nativeUserID string) error {
	if record.ClientMessageID == "" || nativeUserID == "" {
		return nil
	}
	events, err := translator.EmitNativeUserIdentity(record.TurnID, record.ClientMessageID, nativeUserID)
	if err != nil {
		return err
	}
	return writer.write(encodeAGUIEvents(events), nil)
}

// Correlation is a checked durable identity join, never content matching or
// caller metadata accepted as authentication/ownership authority.
func aguiBootstrapUserAliases(ctx context.Context, store aguistore.Store, record *aguistore.Run, transcript *ConversationStateResponse, journal []aguistate.Object) map[string]string {
	starters := map[string]string{}
	for _, turn := range transcript.Conversation.Turns {
		if turn != nil && turn.StartedByMessageID != "" {
			starters[turn.TurnID] = turn.StartedByMessageID
		}
	}
	aliases := map[string]string{}
	for _, message := range journal {
		var identity aguistate.Object
		if message["role"] == "user" {
			metadata := aguiRecoveryObject(message["metadata"])
			namespace := aguiRecoveryObject(metadata["agently"])
			identity = aguiRecoveryObject(namespace["presentation"])
		} else if message["role"] == "activity" && message["activityType"] == "agently.user-identity" {
			identity = aguiRecoveryObject(message["content"])
		}
		if identity == nil || identity["version"] != "1" {
			continue
		}
		runID, _ := identity["protocolRunId"].(string)
		turnID, _ := identity["nativeTurnId"].(string)
		clientID, _ := identity["clientMessageId"].(string)
		if runID == "" || turnID == "" || clientID == "" {
			continue
		}
		run, err := store.GetRun(ctx, record.Principal, record.ThreadID, runID)
		if err != nil || run == nil || run.Principal != record.Principal || run.ThreadID != record.ThreadID || run.TurnID != turnID || run.ClientMessageID != clientID {
			continue
		}
		if message["role"] == "user" && message["id"] != clientID {
			continue
		}
		nativeID := starters[turnID]
		if nativeID != "" {
			aliases[nativeID] = clientID
		}
	}
	return aliases
}

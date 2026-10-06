package extensions

import "testing"

func TestConversationBootstrapExtensionContract(t *testing.T) {
	for _, valid := range []string{`{}`, `{"mode":"live","includeModelCalls":false,"includeToolCalls":false,"includeFeeds":false}`, `{"mode":"transcript","since":"native-message","selectors":{"ExecutionGroup":{"limit":20,"offset":0}}}`} {
		if err := ValidateConversationPayload("conversation.bootstrap", []byte(valid)); err != nil {
			t.Fatalf("valid payload rejected: %v", err)
		}
	}
	for _, invalid := range []string{`{"userId":"forged"}`, `{"conversationId":"foreign"}`, `{"mode":"live","since":"message"}`, `{"selectors":{"Transcript":{"limit":-1}}}`, `{"includeFeeds":null}`, `{} {}`} {
		if err := ValidateConversationPayload("conversation.bootstrap", []byte(invalid)); err == nil {
			t.Fatalf("invalid payload accepted: %s", invalid)
		}
	}
	if err := ValidateConversationEnvelope([]byte(`{"version":"1","operation":"conversation.bootstrap","requestId":"request","target":{"threadId":"thread"},"payload":{}}`)); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{`{"version":"2","operation":"conversation.bootstrap","requestId":"request"}`, `{"version":"1","operation":"conversation.attach","requestId":"request"}`, `{"version":"1","operation":"conversation.bootstrap","requestId":"request","principal":"foreign"}`} {
		if err := ValidateConversationEnvelope([]byte(invalid)); err == nil {
			t.Fatalf("invalid envelope accepted: %s", invalid)
		}
	}
	valid := `{"version":"1","threadId":"thread","transcript":{"schemaVersion":"2","future":{"keep":true}},"messages":[],"state":null,"runs":[{"threadId":"thread","runId":"run","status":"running","revision":1,"lastSequence":0}],"projection":{"lossless":false,"unavailableMessageIds":["native-only"]}}`
	if err := ValidateConversationResult([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	leaking := `{"version":"1","threadId":"thread","transcript":{},"messages":[],"state":null,"runs":[{"threadId":"thread","runId":"run","status":"running","revision":1,"lastSequence":0,"input":{"secret":true}}],"projection":{"lossless":true,"unavailableMessageIds":[]}}`
	if err := ValidateConversationResult([]byte(leaking)); err == nil {
		t.Fatal("accepted input leaked through a run descriptor")
	}
}

package sdk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/runtime/aguistate"
)

type mcpAppJournalIdentity struct {
	Thread   string `json:"t"`
	Run      string `json:"r"`
	Sequence int64  `json:"s"`
	Nonce    string `json:"n"`
}

func mcpAppPublicServerID(app AGUIMCPAppBinding) string {
	if app.PublicServerID != "" {
		return app.PublicServerID
	}
	return app.ServerID
}

// registerMCPAppActivity runs only at the backend journal boundary. The alias
// identifies an exact persisted activity; decoding it never grants authority.
func registerMCPAppActivity(raw json.RawMessage, run *aguistore.Run, sequence int64) (json.RawMessage, error) {
	var event map[string]json.RawMessage
	if json.Unmarshal(raw, &event) != nil {
		return raw, nil
	}
	var kind, activity string
	_ = json.Unmarshal(event["type"], &kind)
	_ = json.Unmarshal(event["activityType"], &activity)
	if kind != "ACTIVITY_SNAPSHOT" || activity != "mcp-apps" {
		return raw, nil
	}
	var content map[string]json.RawMessage
	if err := json.Unmarshal(event["content"], &content); err != nil {
		return nil, err
	}
	var app AGUIMCPAppBinding
	if json.Unmarshal(content["_agentlyApp"], &app) != nil || app.ServerID == "" || app.ServerHash == "" || app.ResourceURI == "" {
		return nil, fmt.Errorf("MCP Apps activity requires backend app binding")
	}
	if app.ThreadID == "" {
		app.ThreadID = run.ConversationID
	}
	if (app.ThreadID != run.ConversationID || app.NativeTurnID != "" && app.NativeTurnID != run.TurnID) && (app.NativeTurnID == "" || app.InvocationID == "") {
		return nil, fmt.Errorf("MCP child app requires registered native invocation identity")
	}
	app.Version = "1"
	app.AppInstanceID = uuid.NewString()
	identity, _ := json.Marshal(mcpAppJournalIdentity{Thread: run.ThreadID, Run: run.RunID, Sequence: sequence, Nonce: app.AppInstanceID})
	app.PublicServerID = "agui-app:" + base64.RawURLEncoding.EncodeToString(identity)
	content["serverId"] = rawAGUI(app.PublicServerID)
	content["_agentlyApp"] = rawAGUI(app)
	event["content"] = rawAGUI(content)
	return json.Marshal(event)
}

// ResolveAGUIMCPApp reads only the current principal's journal. Callers must
// additionally authorize original conversation visibility and app policy.
func ResolveAGUIMCPApp(ctx context.Context, store aguistore.Store, principal, publicServerID, serverHash string) (*AGUIMCPAppBinding, error) {
	if principal == "" || store == nil || !strings.HasPrefix(publicServerID, "agui-app:") {
		return nil, fmt.Errorf("backend-issued MCP app alias is required")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(publicServerID, "agui-app:"))
	var identity mcpAppJournalIdentity
	if err != nil || json.Unmarshal(decoded, &identity) != nil || identity.Thread == "" || identity.Run == "" || identity.Sequence < 1 || identity.Nonce == "" {
		return nil, fmt.Errorf("invalid MCP app alias")
	}
	run, err := store.GetRun(ctx, principal, identity.Thread, identity.Run)
	if err != nil {
		return nil, err
	}
	if run.Principal != principal || run.Status == aguistore.StatusCancelled || run.Status == aguistore.StatusError {
		return nil, fmt.Errorf("MCP app binding unavailable")
	}
	events, err := store.Replay(ctx, principal, identity.Thread, identity.Run, identity.Sequence-1, 1)
	if err != nil {
		return nil, err
	}
	if len(events) != 1 || events[0].Sequence != identity.Sequence {
		return nil, fmt.Errorf("MCP app activity unavailable")
	}
	var event struct {
		Type     string `json:"type"`
		Activity string `json:"activityType"`
		Content  struct {
			Binding     AGUIMCPAppBinding `json:"_agentlyApp"`
			ServerID    string            `json:"serverId"`
			ServerHash  string            `json:"serverHash"`
			ResourceURI string            `json:"resourceUri"`
		} `json:"content"`
	}
	if json.Unmarshal(events[0].Event, &event) != nil {
		return nil, fmt.Errorf("invalid MCP app activity")
	}
	app := event.Content.Binding
	if event.Type != "ACTIVITY_SNAPSHOT" || event.Activity != "mcp-apps" || app.Version != "1" || app.ThreadID == "" || app.AppInstanceID != identity.Nonce || app.PublicServerID != publicServerID || event.Content.ServerID != publicServerID || app.ServerID == "" || app.ServerHash != serverHash || event.Content.ServerHash != app.ServerHash || app.ResourceURI == "" || event.Content.ResourceURI != app.ResourceURI {
		return nil, fmt.Errorf("MCP app binding does not match registered activity")
	}
	if err = validateMCPAppJournalAncestry(ctx, store, run, events[0].Event, identity.Sequence); err != nil {
		return nil, err
	}
	thread, err := store.GetThread(ctx, principal, identity.Thread)
	if err != nil {
		return nil, err
	}
	var current []map[string]json.RawMessage
	if json.Unmarshal(thread.Messages, &current) != nil {
		return nil, fmt.Errorf("MCP app current binding unavailable")
	}
	for _, message := range current {
		var role, activity string
		_ = json.Unmarshal(message["role"], &role)
		_ = json.Unmarshal(message["activityType"], &activity)
		if role != "activity" || activity != "mcp-apps" {
			continue
		}
		var content struct {
			Binding     AGUIMCPAppBinding `json:"_agentlyApp"`
			ServerID    string            `json:"serverId"`
			ServerHash  string            `json:"serverHash"`
			ResourceURI string            `json:"resourceUri"`
		}
		if json.Unmarshal(message["content"], &content) != nil {
			continue
		}
		if content.Binding == app && content.ServerID == publicServerID && content.ServerHash == app.ServerHash && content.ResourceURI == app.ResourceURI {
			return &app, nil
		}
	}
	return nil, fmt.Errorf("MCP app binding was removed or revoked")
}

// A native translator keeps its pre-registration activity vocabulary. Preserve
// already issued app aliases when it later publishes an aggregate history
// snapshot; otherwise the renderer would regress to a native server identifier.
func preserveMCPAppSnapshotAliases(raw json.RawMessage, messages []aguistate.Object) (json.RawMessage, error) {
	var event map[string]json.RawMessage
	if json.Unmarshal(raw, &event) != nil {
		return raw, nil
	}
	var kind string
	_ = json.Unmarshal(event["type"], &kind)
	if kind != "MESSAGES_SNAPSHOT" {
		return raw, nil
	}
	var incoming []map[string]json.RawMessage
	if err := json.Unmarshal(event["messages"], &incoming); err != nil {
		return nil, err
	}
	existing := map[string]aguistate.Object{}
	for _, message := range messages {
		if id, ok := message["id"].(string); ok {
			existing[id] = message
		}
	}
	for _, message := range incoming {
		var id, activity string
		_ = json.Unmarshal(message["id"], &id)
		_ = json.Unmarshal(message["activityType"], &activity)
		if activity != "mcp-apps" || existing[id] == nil {
			continue
		}
		var current struct {
			Binding AGUIMCPAppBinding `json:"_agentlyApp"`
		}
		if json.Unmarshal(rawAGUI(existing[id]["content"]), &current) != nil || current.Binding.PublicServerID == "" || current.Binding.Version != "1" {
			continue
		}
		var content map[string]json.RawMessage
		if err := json.Unmarshal(message["content"], &content); err != nil {
			return nil, err
		}
		var source AGUIMCPAppBinding
		if json.Unmarshal(content["_agentlyApp"], &source) != nil || source.ServerID != current.Binding.ServerID || source.ServerHash != current.Binding.ServerHash || source.ResourceURI != current.Binding.ResourceURI || source.ThreadID != current.Binding.ThreadID || source.NativeTurnID != current.Binding.NativeTurnID || source.InvocationID != current.Binding.InvocationID {
			return nil, fmt.Errorf("MCP app history snapshot differs from registered binding")
		}
		content["serverId"] = rawAGUI(current.Binding.PublicServerID)
		content["_agentlyApp"] = rawAGUI(current.Binding)
		message["content"] = rawAGUI(content)
	}
	event["messages"] = rawAGUI(incoming)
	return json.Marshal(event)
}

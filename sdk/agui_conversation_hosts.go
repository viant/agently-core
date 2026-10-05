package sdk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	aguistore "github.com/viant/agently-core/app/store/agui"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/runtime/aguistate"
)

// Host activities are a separate authorized renderer surface. They must never
// be merged into bootstrap.Messages or a later model request.
func aguiBootstrapHostActivities(ctx context.Context, client Client, store aguistore.Store, record *aguistore.Run, messages []aguistate.Object) ([]aguistate.Object, []string) {
	result := []aguistate.Object{}
	unavailable := []string{}
	bindings, _ := ctx.Value(aguiWorkspaceBindingsKey{}).(AGUIWorkspaceBindings)
	host := bindings.MCPApps
	if host == nil {
		if provider, ok := client.(interface{ aguiMCPAppsHost() *AGUIMCPAppsHost }); ok {
			host = provider.aguiMCPAppsHost()
		}
	}
	for _, message := range messages {
		if message["role"] != "activity" || message["activityType"] != "mcp-apps" {
			continue
		}
		id, _ := message["id"].(string)
		var content struct {
			ServerID    string `json:"serverId"`
			ServerHash  string `json:"serverHash"`
			ResourceURI string `json:"resourceUri"`
		}
		if record == nil || record.Principal == "" || iauth.EffectiveUserID(ctx) != record.Principal || host == nil || json.Unmarshal(rawAGUI(message["content"]), &content) != nil {
			unavailable = append(unavailable, id)
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(content.ServerID, "agui-app:"))
		var identity mcpAppJournalIdentity
		if err != nil || json.Unmarshal(raw, &identity) != nil || identity.Thread != record.ThreadID {
			unavailable = append(unavailable, id)
			continue
		}
		app, err := ResolveAGUIMCPApp(ctx, store, record.Principal, content.ServerID, content.ServerHash)
		if err != nil || app.ResourceURI != content.ResourceURI {
			unavailable = append(unavailable, id)
			continue
		}
		scoped := host.Bind(ctx, *app)
		if scoped.Authorize == nil || scoped.Authorize(ctx, *app) != nil {
			unavailable = append(unavailable, id)
			continue
		}
		result = append(result, message)
	}
	return result, unavailable
}

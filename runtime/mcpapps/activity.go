package mcpapps

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
)

type AppBinding struct {
	AppInstanceID  string `json:"appInstanceId"`
	PublicServerID string `json:"publicServerId"`
	Version        string `json:"version"`
	ThreadID       string `json:"threadId"`
	NativeTurnID   string `json:"nativeTurnId,omitempty"`
	InvocationID   string `json:"invocationId,omitempty"`
	ServerID       string `json:"nativeServerId"`
	ServerHash     string `json:"serverHash"`
	ResourceURI    string `json:"resourceUri"`
}

type activityEmitterKey struct{}
type ActivityEmitter func(context.Context, AppBinding, json.RawMessage, json.RawMessage) error

func WithActivityEmitter(ctx context.Context, emit ActivityEmitter) context.Context {
	return context.WithValue(ctx, activityEmitterKey{}, emit)
}
func HasActivityEmitter(ctx context.Context) bool {
	emit, _ := ctx.Value(activityEmitterKey{}).(ActivityEmitter)
	return emit != nil
}
func EmitActivity(ctx context.Context, app AppBinding, result, args json.RawMessage) error {
	emit, _ := ctx.Value(activityEmitterKey{}).(ActivityEmitter)
	if emit == nil {
		return nil
	}
	return emit(ctx, app, result, args)
}
func Activity(messageID string, app AppBinding, result, args json.RawMessage) (json.RawMessage, error) {
	if app.ServerID == "" || app.ServerHash == "" || app.ResourceURI == "" || !json.Valid(result) {
		return nil, fmt.Errorf("MCP Apps activity binding/result is incomplete")
	}
	serverID := app.PublicServerID
	if serverID == "" {
		serverID = app.ServerID
	}
	content := map[string]any{"result": result, "resourceUri": app.ResourceURI, "serverHash": app.ServerHash, "serverId": serverID, "_agentlyApp": app}
	if len(args) > 0 {
		if !json.Valid(args) {
			return nil, fmt.Errorf("invalid MCP Apps toolInput")
		}
		content["toolInput"] = args
	}
	return json.Marshal(map[string]any{"type": "ACTIVITY_SNAPSHOT", "messageId": messageID, "activityType": "mcp-apps", "content": content, "replace": true})
}
func ServerHash(transport, endpoint string) (string, error) {
	if transport == "streamable" {
		transport = "http"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.User != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || (transport != "http" && transport != "sse") {
		return "", fmt.Errorf("MCP Apps endpoint must be configured HTTP(S) without embedded credentials")
	}
	data, _ := json.Marshal(struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	}{transport, endpoint})
	digest := md5.Sum(data)
	return hex.EncodeToString(digest[:]), nil
}

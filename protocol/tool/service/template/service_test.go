package template

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	tpldef "github.com/viant/agently-core/protocol/template"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
	tplbundlerepo "github.com/viant/agently-core/workspace/repository/templatebundle"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

func TestServiceListFiltersTemplatesByConversationClientTarget(t *testing.T) {
	root := t.TempDir()
	store := fsstore.New(root)
	templates := tplrepo.NewWithStore(store)
	bundles := tplbundlerepo.NewWithStore(store)

	ctx := context.Background()
	err := templates.Save(ctx, "web_only", &tpldef.Template{
		ID:          "web_only",
		Name:        "web_only",
		Description: "web-only template",
		Platforms:   []string{"web"},
		FormFactors: []string{"desktop"},
		Surfaces:    []string{"browser"},
	})
	if err != nil {
		t.Fatalf("save web template: %v", err)
	}
	err = templates.Save(ctx, "android_only", &tpldef.Template{
		ID:          "android_only",
		Name:        "android_only",
		Description: "android-only template",
		Platforms:   []string{"android"},
		FormFactors: []string{"phone"},
		Surfaces:    []string{"app"},
	})
	if err != nil {
		t.Fatalf("save android template: %v", err)
	}

	metaBytes, _ := json.Marshal(map[string]interface{}{
		"context": map[string]interface{}{
			"client": map[string]interface{}{
				"platform":   "web",
				"formFactor": "desktop",
				"surface":    "browser",
			},
		},
	})
	meta := string(metaBytes)
	conv := &apiconv.Conversation{Id: "c1", Metadata: &meta}
	svc := New(templates, bundles, WithConversationClient(stubConversationClient{conversation: conv}))
	toolCtx := runtimerequestctx.WithConversationID(context.Background(), "c1")

	out := &ListOutput{}
	if err := svc.list(toolCtx, &ListInput{}, out); err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("expected 1 filtered template, got %d", len(out.Items))
	}
	if got := out.Items[0].Name; got != "web_only" {
		t.Fatalf("unexpected template %q", got)
	}

	if _, err := os.Stat(filepath.Join(root, "templates", "web_only.yaml")); err != nil {
		t.Fatalf("expected saved template file: %v", err)
	}
}

type stubConversationClient struct {
	conversation *apiconv.Conversation
}

func (s stubConversationClient) GetConversation(context.Context, string, ...apiconv.Option) (*apiconv.Conversation, error) {
	return s.conversation, nil
}
func (s stubConversationClient) GetConversations(context.Context, *apiconv.Input) ([]*apiconv.Conversation, error) {
	panic("unexpected call")
}
func (s stubConversationClient) PatchConversations(context.Context, *apiconv.MutableConversation) error {
	panic("unexpected call")
}
func (s stubConversationClient) GetPayload(context.Context, string) (*apiconv.Payload, error) {
	panic("unexpected call")
}
func (s stubConversationClient) PatchPayload(context.Context, *apiconv.MutablePayload) error {
	panic("unexpected call")
}
func (s stubConversationClient) PatchMessage(context.Context, *apiconv.MutableMessage) error {
	panic("unexpected call")
}
func (s stubConversationClient) GetMessage(context.Context, string, ...apiconv.Option) (*apiconv.Message, error) {
	panic("unexpected call")
}
func (s stubConversationClient) GetMessageByElicitation(context.Context, string, string) (*apiconv.Message, error) {
	panic("unexpected call")
}
func (s stubConversationClient) PatchModelCall(context.Context, *apiconv.MutableModelCall) error {
	panic("unexpected call")
}
func (s stubConversationClient) PatchToolCall(context.Context, *apiconv.MutableToolCall) error {
	panic("unexpected call")
}
func (s stubConversationClient) PatchTurn(context.Context, *apiconv.MutableTurn) error {
	panic("unexpected call")
}
func (s stubConversationClient) DeleteConversation(context.Context, string) error {
	panic("unexpected call")
}
func (s stubConversationClient) DeleteMessage(context.Context, string, string) error {
	panic("unexpected call")
}

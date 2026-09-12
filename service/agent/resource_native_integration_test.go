package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"
	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	openaip "github.com/viant/agently-core/genai/llm/provider/openai"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/protocol/tool/service/resources"
	scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
	"github.com/viant/agently-core/service/shared/toolexec"
)

type resourceIntegrationRegistry struct {
	fakeRegistry
	service *resources.Service
}

func (r *resourceIntegrationRegistry) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	var input, out interface{}
	if strings.HasSuffix(name, "readImage") {
		input = &resources.ReadImageInput{}
		out = &resources.ReadImageOutput{}
	} else {
		input = &resources.ReadInput{}
		out = &resources.ReadOutput{}
	}
	data, _ := json.Marshal(args)
	if err := json.Unmarshal(data, input); err != nil {
		return "", err
	}
	method := "read"
	if strings.HasSuffix(name, "readImage") {
		method = "readImage"
	}
	exec, err := r.service.Method(method)
	if err != nil {
		return "", err
	}
	if err = exec(ctx, input, out); err != nil {
		return "", err
	}
	data, err = json.Marshal(out)
	return string(data), err
}
func TestResourceToolToOpenAINativeRequest(t *testing.T) {
	for _, kind := range []string{"image", "pdf"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv(scratchpadsvc.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
			ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
			store := convmem.New()
			c := apiconv.NewConversation()
			c.SetId("conv")
			require.NoError(t, store.PatchConversations(ctx, c))
			turn := requestctx.TurnMeta{ConversationID: "conv", TurnID: "turn", ParentMessageID: "turn"}
			ctx = requestctx.WithTurnMeta(ctx, turn)
			_, err := apiconv.AddMessage(ctx, store, &turn, apiconv.WithId("turn"), apiconv.WithRole("user"), apiconv.WithType("text"), apiconv.WithContent("Read this resource"))
			require.NoError(t, err)
			var b bytes.Buffer
			mime := "image/png"
			name := "image.png"
			toolName := "resources-readImage"
			args := map[string]interface{}{}
			if kind == "image" {
				require.NoError(t, png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 10, 10))))
			} else {
				mime = "application/pdf"
				name = "report.pdf"
				toolName = "resources-read"
				args["representation"] = "native"
				f := fpdf.New("P", "mm", "A4", "")
				f.AddPage()
				f.SetFont("Arial", "", 12)
				f.Cell(40, 10, "Native PDF must remain a file")
				require.NoError(t, f.Output(&b))
			}
			d, err := scratchpadsvc.New().PublishArtifact(ctx, "", name, mime, "", bytes.NewReader(b.Bytes()))
			require.NoError(t, err)
			args["uri"] = d.URI
			reg := &resourceIntegrationRegistry{service: resources.New(nil)}
			_, _, err = toolexec.ExecuteToolStep(ctx, reg, toolexec.StepInfo{ID: "call", Name: toolName, Args: args, ResponseID: "response"}, store)
			require.NoError(t, err)
			conv, err := store.GetConversation(ctx, "conv")
			require.NoError(t, err)
			svc := &Service{conversation: store}
			history, err := svc.buildHistory(ctx, conv.GetTranscript())
			require.NoError(t, err)
			input := &core.GenerateInput{ModelSelection: llm.ModelSelection{Model: "gpt-4o"}, UserID: "alice", Prompt: &binding.Prompt{Text: "{{.Task.Prompt}}", Engine: "go"}, Binding: &binding.Binding{Task: binding.Task{Prompt: "Read this resource"}, History: history}}
			require.NoError(t, input.Init(ctx))
			messages := input.Message
			binary := false
			for _, m := range messages {
				for _, item := range m.Items {
					if item.Type == llm.ContentTypeBinary {
						binary = true
						require.Equal(t, mime, item.MimeType)
					}
				}
			}
			require.True(t, binary, "history must contain native bytes")
			client := &openaip.Client{}
			req, err := client.ToRequestContext(ctx, &llm.GenerateRequest{Messages: messages, Options: &llm.Options{Model: "gpt-4o", Metadata: map[string]interface{}{"attachMode": "inline"}}})
			require.NoError(t, err)
			wire := openaip.ToResponsesPayload(req)
			raw, err := json.Marshal(wire)
			require.NoError(t, err)
			want := "input_image"
			if kind == "pdf" {
				want = "input_file"
			}
			require.Contains(t, string(raw), `"type":"`+want+`"`)
		})
	}
}

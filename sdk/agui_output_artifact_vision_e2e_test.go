package sdk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	conversation "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	openaip "github.com/viant/agently-core/genai/llm/provider/openai"
	iauth "github.com/viant/agently-core/internal/auth"
	toolregistry "github.com/viant/agently-core/internal/tool/registry"
	agentmodel "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/protocol/binding"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/tool/service/resources"
	scratchpad "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/runtime/requestctx"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/service/browsermcp"
	"github.com/viant/agently-core/service/core"
	"github.com/viant/agently-core/service/shared/toolexec"
)

// Exercise the real reverse-result, Datly persistence, scratchpad ownership,
// resource tool, native attachment/history and provider serialization paths.
// Only external model decisions and the OS screenshot bytes are deterministic.
func TestBrowserCaptureAdmissionToOwnedReadImageAndResponsesVision(t *testing.T) {
	ctx, c, old, options := artifactHTTPServer(t)
	old.Close()
	c.nativeCompletion = true
	options.OutputArtifacts = map[string]mcpcfg.OutputArtifact{"mechanize_capture": {Name: "screenshot.png", Role: "screenshot"}}
	options.BrowserTransport = &mcpcfg.BrowserTransport{Type: "chrome-extension", ExtensionID: strings.Repeat("a", 32), PortName: "fixture"}
	descriptor, err := options.BrowserDescriptor("mechanize")
	require.NoError(t, err)
	catalogs := browsermcp.New(func(context.Context) ([]mcpcfg.BrowserDescriptor, error) {
		return []mcpcfg.BrowserDescriptor{descriptor}, nil
	})
	catalog, err := catalogs.Register(ctx, "owner", browsermcp.Registration{ConversationID: "thread", ThreadID: "thread", ConnectionID: "original-connection", Server: "mechanize", Tools: []browsermcp.Tool{{Name: "mechanize_capture", InputSchema: json.RawMessage(`{"type":"object"}`)}}})
	require.NoError(t, err)
	tool := catalog.Tools[0]
	var captureCall clienttool.PendingCall
	c.query = func(ctx context.Context, input *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		turn := conversation.NewTurn()
		turn.SetId(input.MessageID)
		turn.SetConversationID(input.ConversationID)
		turn.SetStatus("waiting_for_user")
		if err := c.nativeConversation.PatchTurn(ctx, turn); err != nil {
			return nil, err
		}
		for _, row := range []struct{ id, role, content string }{{input.MessageID, "user", "Describe the public screenshot"}, {"capture-assistant", "assistant", ""}} {
			message := conversation.NewMessage()
			message.SetId(row.id)
			message.SetConversationID(input.ConversationID)
			message.SetTurnID(input.MessageID)
			message.SetRole(row.role)
			message.SetType("text")
			message.SetContent(row.content)
			if err := c.nativeConversation.PatchMessage(ctx, message); err != nil {
				return nil, err
			}
		}
		ctx = requestctx.WithModelMessageID(requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: input.ConversationID, TurnID: input.MessageID}), "capture-assistant")
		if _, _, err := toolexec.ExecuteToolStep(ctx, nil, toolexec.StepInfo{ID: "capture-call", Name: tool.Name, Args: map[string]interface{}{}}, c.nativeConversation); err != nil {
			return nil, err
		}
		pending := clienttool.FromContext(ctx).Pending()
		captureCall = pending[0]
		return &agentsvc.QueryOutput{ExecutionStatus: "waiting_for_user", ClientToolCalls: pending}, nil
	}
	reg, err := toolregistry.NewWithManager(c.normalizer.mcpMgr)
	require.NoError(t, err)
	require.NoError(t, reg.AddInternalService(resources.New(nil)))
	var publishedURI string
	var providerHasImage bool
	var providerWidth, providerHeight, providerImageCount int
	var providerDetail, imagePrefix string
	c.resumeNative = func(ctx context.Context, input *agentsvc.QueryInput, turnID string, output *agentsvc.QueryOutput) error {
		var captured struct {
			Resources []struct {
				URI string `json:"uri"`
			} `json:"resources"`
		}
		if err := json.Unmarshal([]byte(c.completedContent), &captured); err != nil {
			return err
		}
		if len(captured.Resources) != 1 {
			t.Fatal("capture did not publish one owned image")
		}
		publishedURI = captured.Resources[0].URI
		ctx = requestctx.WithModelMessageID(requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: input.ConversationID, TurnID: turnID, ParentMessageID: turnID}), "capture-assistant")
		if _, _, err := toolexec.ExecuteToolStep(ctx, reg, toolexec.StepInfo{ID: "read-image-call", Name: "resources-readImage", Args: map[string]interface{}{"uri": publishedURI, "maxWidth": 384, "maxHeight": 256, "format": "png"}}, c.nativeConversation); err != nil {
			return err
		}
		agent := &agentmodel.Agent{Identity: agentmodel.Identity{ID: "fixture"}, ModelSelection: llm.ModelSelection{Model: "gpt-6.1-sol"}, Prompt: &binding.Prompt{Text: "Describe the screenshot", Engine: "go"}}
		service := agentsvc.New(nil, nil, nil, reg, &config.Defaults{}, c.nativeConversation)
		bound, err := service.BuildBinding(ctx, &agentsvc.QueryInput{ConversationID: input.ConversationID, UserId: "owner", Agent: agent, Query: "Describe the screenshot"})
		if err != nil {
			return err
		}
		generated := &core.GenerateInput{ModelSelection: agent.ModelSelection, UserID: "owner", Prompt: agent.Prompt, Binding: bound}
		if err := generated.Init(ctx); err != nil {
			return err
		}
		request, err := (&openaip.Client{}).ToRequestContext(ctx, &llm.GenerateRequest{Messages: generated.Message, Options: &llm.Options{Model: "gpt-6.1-sol", Metadata: map[string]interface{}{"attachMode": "inline"}}})
		if err != nil {
			return err
		}
		provider := openaip.ToResponsesPayload(request)
		for _, item := range provider.Input {
			if strings.Contains(item.Output, imagePrefix) {
				t.Fatal("image bytes became text function output")
			}
			for _, part := range append(item.Content, item.OutputParts...) {
				if part.Type != "input_image" {
					if strings.Contains(part.Text, imagePrefix) {
						t.Fatal("image bytes became a text content item")
					}
					continue
				}
				providerImageCount++
				providerDetail = part.Detail
				data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(part.ImageURL, "data:image/png;base64,"))
				if err != nil {
					return err
				}
				size, err := png.DecodeConfig(bytes.NewReader(data))
				if err != nil {
					return err
				}
				providerWidth, providerHeight = size.Width, size.Height
			}
		}
		providerHasImage = providerImageCount == 1
		output.ConversationID, output.TurnID, output.Content = input.ConversationID, turnID, "Public screenshot described"
		return nil
	}
	handler := handleAGUIRun(c, nil, AGUIWorkspaceBindings{BrowserMCP: catalogs})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: "owner"})))
	}))
	defer server.Close()
	chat := agui.RunAgentInput{ThreadID: "thread", RunID: "capture-run", Tools: catalog.Tools, Messages: []agui.Message{{ID: "request", Role: "user", Content: rawAGUI("Describe the public screenshot")}}}
	status, wire := durablePost(t, server, string(rawAGUI(chat)), nil)
	require.Equal(t, http.StatusOK, status)
	require.False(t, strings.Contains(wire, "RUN_ERROR"), "capture admission failed")
	original, err := c.store.GetRun(ctx, "owner", "thread", "capture-run")
	require.NoError(t, err)
	picture := image.NewNRGBA(image.Rect(0, 0, 384, 256))
	_, _ = rand.New(rand.NewSource(19)).Read(picture.Pix)
	for index := 3; index < len(picture.Pix); index += 4 {
		picture.Pix[index] = 255
	}
	var pngBytes bytes.Buffer
	require.NoError(t, png.Encode(&pngBytes, picture))
	pixels := pngBytes.Bytes()
	require.Greater(t, len(pixels), 96*1024, "must exercise the live controller's former small-image cutoff")
	require.Less(t, len(pixels), 2<<20)
	encoded := base64.StdEncoding.EncodeToString(pixels)
	imagePrefix = encoded[:128]
	digest := sha256.Sum256(pixels)
	capture := map[string]any{"artifact": map[string]any{"id": "OS-CAPTURE-REFERENCE", "mediaType": "image/png", "sizeBytes": len(pixels), "contentHash": hex.EncodeToString(digest[:])}, "metadata": map[string]any{"width": 384, "height": 256, "pid": 123, "windowId": 7, "bundleId": "com.example.Browser", "identity": "window:7", "bytes": len(pixels)}, "imageReturned": true, "imageIsPreview": false, "inlineByteLimit": 2 << 20}
	canonical := rawAGUI(map[string]any{"content": []any{map[string]any{"type": "text", "text": string(rawAGUI(capture))}, map[string]any{"type": "image", "mimeType": "image/png", "data": encoded}}, "structuredContent": capture})
	answer := agui.RunAgentInput{ThreadID: "thread", RunID: "capture-answer", ParentRunID: original.RunID, Tools: catalog.Tools, Messages: []agui.Message{{ID: "answer", Role: "tool", ToolCallID: agui.ProtocolToolCallID(original.TurnID, "capture-call"), Content: rawAGUI(string(canonical)), Metadata: tool.Metadata}}}
	status, wire = durablePost(t, server, string(rawAGUI(answer)), nil)
	require.Equal(t, http.StatusOK, status)
	require.False(t, strings.Contains(wire, "RUN_ERROR"), "capture continuation failed")
	require.True(t, providerHasImage, "provider never received input_image")
	require.Equal(t, 384, providerWidth)
	require.Equal(t, 256, providerHeight)
	require.Equal(t, "high", providerDetail, "classified screenshot detail was not enforced")
	t.Logf("provider input_image count=%d dimensions=%dx%d detail=high; no base64 text", providerImageCount, providerWidth, providerHeight)
	require.True(t, strings.HasPrefix(publishedURI, "scratchpad://artifact/"))
	_, err = scratchpad.New().ReadArtifactPayload(iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "foreign"}), publishedURI)
	require.Error(t, err, "foreign principal read the capture")
	accepted, err := c.store.GetRun(ctx, "owner", "thread", "capture-answer")
	require.NoError(t, err)
	nativeCapture, err := c.nativeConversation.GetMessage(ctx, captureCall.ToolMessageID, conversation.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.NotNil(t, nativeCapture)
	for _, body := range [][]byte{accepted.Input, []byte(c.completedContent), []byte(wire), []byte(nativeCapture.GetContent())} {
		require.False(t, bytes.Contains(body, []byte(encoded[:128])), "original image leaked into capture output")
		require.False(t, bytes.Contains(body, []byte("OS-CAPTURE-REFERENCE")), "untrusted native artifact reference leaked")
	}
	before, _, err := scratchpad.New().ListArtifacts(ctx, "", 100)
	require.NoError(t, err)
	status, replay := durablePost(t, server, string(rawAGUI(answer)), nil)
	require.Equal(t, http.StatusOK, status)
	require.True(t, wire == replay, "idempotent admission changed its journal")
	require.EqualValues(t, 1, c.queries.Load())
	require.EqualValues(t, 1, c.normalizations.Load())
	require.EqualValues(t, 1, c.completions.Load())
	require.EqualValues(t, 1, c.resumes.Load())
	after, _, err := scratchpad.New().ListArtifacts(ctx, "", 100)
	require.NoError(t, err)
	require.Len(t, after, len(before), "replay published another image")
}

func TestScreenshotReadImageProjectionKeepsLatestClassifiedImagesAndOwnedReferences(t *testing.T) {
	ctx, c, server, _ := artifactHTTPServer(t)
	server.Close()
	turn := conversation.NewTurn()
	turn.SetId("snapshot-turn")
	turn.SetConversationID("thread")
	turn.SetStatus("running")
	require.NoError(t, c.nativeConversation.PatchTurn(ctx, turn))
	user := conversation.NewMessage()
	user.SetId("snapshot-turn")
	user.SetConversationID("thread")
	user.SetTurnID("snapshot-turn")
	user.SetRole("user")
	user.SetType("text")
	user.SetContent("Describe the current screenshots and the user image")
	require.NoError(t, c.nativeConversation.PatchMessage(ctx, user))
	ctx = requestctx.WithModelMessageID(requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: "thread", TurnID: "snapshot-turn"}), "snapshot-assistant")
	reg, err := toolregistry.NewWithManager(c.normalizer.mcpMgr)
	require.NoError(t, err)
	require.NoError(t, reg.AddInternalService(resources.New(nil)))
	var originals []*scratchpad.ArtifactDescriptor
	var sourceBytes [][]byte
	for index, role := range []string{"screenshot", "screenshot", "screenshot", ""} {
		picture := image.NewRGBA(image.Rect(0, 0, 64, 32))
		for y := 0; y < 32; y++ {
			for x := 0; x < 64; x++ {
				picture.Set(x, y, color.RGBA{R: byte(40 * (index + 1)), G: 90, A: 255})
			}
		}
		var encoded bytes.Buffer
		require.NoError(t, png.Encode(&encoded, picture))
		original, err := scratchpad.New().PublishArtifact(ctx, "", "screenshot.png", "image/png", "", bytes.NewReader(encoded.Bytes()), role)
		require.NoError(t, err)
		originals = append(originals, original)
		sourceBytes = append(sourceBytes, append([]byte(nil), encoded.Bytes()...))
		_, _, err = toolexec.ExecuteToolStep(ctx, reg, toolexec.StepInfo{ID: fmt.Sprintf("read-%d", index), Name: "resources-readImage", Args: map[string]interface{}{"uri": original.URI}}, c.nativeConversation)
		require.NoError(t, err)
	}
	defaults := &config.Defaults{}
	service := agentsvc.New(nil, nil, nil, reg, defaults, c.nativeConversation)
	agent := &agentmodel.Agent{Identity: agentmodel.Identity{ID: "fixture"}, ModelSelection: llm.ModelSelection{Model: "gpt-6.1-sol"}, Prompt: &binding.Prompt{Text: "Describe current evidence", Engine: "go"}}
	project := func() (*binding.Binding, []llm.Message) {
		t.Helper()
		bound, err := service.BuildBinding(ctx, &agentsvc.QueryInput{ConversationID: "thread", MessageID: "snapshot-turn", Agent: agent, Query: "Describe current evidence"})
		require.NoError(t, err)
		generated := &core.GenerateInput{ModelSelection: agent.ModelSelection, UserID: "owner", Prompt: agent.Prompt, Binding: bound}
		require.NoError(t, generated.Init(ctx))
		return bound, generated.Message
	}
	assertImages := func(messages []llm.Message, count, highCount int) {
		t.Helper()
		request, err := (&openaip.Client{}).ToRequestContext(ctx, &llm.GenerateRequest{Messages: messages, Options: &llm.Options{Model: "gpt-6.1-sol", Metadata: map[string]interface{}{"attachMode": "inline"}}})
		require.NoError(t, err)
		images, high := 0, 0
		for _, input := range openaip.ToResponsesPayload(request).Input {
			for _, item := range append(input.Content, input.OutputParts...) {
				if item.Type == "input_image" {
					images++
					if item.Detail == "high" {
						high++
					}
				}
			}
		}
		require.Equal(t, count, images)
		require.Equal(t, highCount, high)
	}
	before, err := c.nativeConversation.GetConversation(ctx, "thread", conversation.WithIncludeTranscript(true), conversation.WithIncludeToolCall(true))
	require.NoError(t, err)
	durableBefore := rawAGUI(before)
	bound, messages := project()
	assertImages(messages, 3, 2)
	active := map[string]bool{}
	for _, attachment := range bound.History.Current.Messages[0].Attachment {
		active[attachment.ResourceURI] = !attachment.OmitModelBytes
	}
	require.False(t, active[originals[0].URI], "oldest classified screenshot still carries active bytes")
	require.True(t, active[originals[1].URI])
	require.True(t, active[originals[2].URI])
	require.True(t, active[originals[3].URI], "ordinary image was classified by its screenshot filename")
	text := ""
	for _, message := range messages {
		text += message.Content
	}
	for _, original := range originals {
		require.True(t, strings.Contains(text, original.URI), "owned artifact reference disappeared")
	}
	after, err := c.nativeConversation.GetConversation(ctx, "thread", conversation.WithIncludeTranscript(true), conversation.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.True(t, bytes.Equal(durableBefore, rawAGUI(after)), "active-context projection mutated durable transcript")
	for index, original := range originals {
		stored, err := scratchpad.New().ReadArtifactPayload(ctx, original.URI)
		require.NoError(t, err)
		require.True(t, bytes.Equal(sourceBytes[index], stored), "retention changed original image bytes")
	}
	// An explicit read of older evidence makes it current again. Repeated reads
	// of the same owned artifact contribute only one active screenshot.
	for _, callID := range []string{"read-older", "read-older-again"} {
		_, _, err := toolexec.ExecuteToolStep(ctx, reg, toolexec.StepInfo{ID: callID, Name: "resources-readImage", Args: map[string]interface{}{"uri": originals[0].URI}}, c.nativeConversation)
		require.NoError(t, err)
	}
	bound, messages = project()
	assertImages(messages, 3, 2)
	oldActive := false
	for _, attachment := range bound.History.Current.Messages[0].Attachment {
		if attachment.ResourceURI == originals[0].URI && !attachment.OmitModelBytes {
			oldActive = true
		}
	}
	require.True(t, oldActive, "explicit older screenshot read did not return to active context")
	limit := 1
	defaults.Projection.ScreenshotRetention = &config.ScreenshotRetention{MaxImages: &limit}
	_, messages = project()
	assertImages(messages, 2, 1)
	disabled := false
	defaults.Projection.ScreenshotRetention.Enabled = &disabled
	_, messages = project()
	assertImages(messages, 4, 3)
}

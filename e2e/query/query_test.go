package query

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/app/executor/config"
	"github.com/viant/agently-core/genai/llm/provider"
	modelfinder "github.com/viant/agently-core/internal/finder/model"
	agentfinder "github.com/viant/agently-core/protocol/agent/finder"
	agentloader "github.com/viant/agently-core/protocol/agent/loader"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	mcpmgr "github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/protocol/prompt"
	"github.com/viant/agently-core/protocol/tool"
	llmagents "github.com/viant/agently-core/protocol/tool/service/llm/agents"
	"github.com/viant/agently-core/sdk"
	agentsvc "github.com/viant/agently-core/service/agent"
	elicrouter "github.com/viant/agently-core/service/elicitation/router"
	wsfs "github.com/viant/agently-core/workspace/loader/fs"
	modelloader "github.com/viant/agently-core/workspace/loader/model"
	meta "github.com/viant/agently-core/workspace/service/meta"
)

// stubMCPProvider satisfies mcpmgr.Provider for tests that don't use MCP servers.
type stubMCPProvider struct{}

func (s *stubMCPProvider) Options(_ context.Context, _ string) (*mcpcfg.MCPClient, error) {
	return nil, fmt.Errorf("no MCP servers configured in test")
}

func skipIfNoAPIKey(t *testing.T) {
	t.Helper()
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set; skipping e2e query test")
	}
}

// setupSDK creates an in-memory embedded SDK client backed by the testdata workspace.
func setupSDK(t *testing.T) sdk.Client {
	t.Helper()
	ctx := context.Background()

	// 1. Use a temp dir as the runtime workspace for db/index files.
	//    Agent/model configs are loaded from the embedded testdata via embed.FS.
	tmp := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", tmp)
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")

	// 2. Resolve testdata path (go test runs from package dir)
	testdataDir, err := filepath.Abs("testdata")
	require.NoError(t, err, "resolve testdata path")
	prepareWorkspaceForEmbeddedE2E(t, testdataDir, tmp)

	// 3. Agent loader from testdata (loader adds agents/ prefix)
	fs := afs.New()
	wsMeta := meta.New(fs, testdataDir)
	agentLdr := agentloader.New(agentloader.WithMetaService(wsMeta))
	agentFndr := agentfinder.New(agentfinder.WithLoader(agentLdr))

	// 4. Model finder from testdata (loader adds models/ prefix)
	modelMeta := wsMeta
	modelLdr := modelloader.New(wsfs.WithMetaService[provider.Config](modelMeta))
	modelFndr := modelfinder.New(modelfinder.WithConfigLoader(modelLdr))

	// 5. MCP manager (stub) and tool registry
	mcpMgr, err := mcpmgr.New(&stubMCPProvider{})
	require.NoError(t, err, "create MCP manager")
	registry, err := tool.NewDefaultRegistry(mcpMgr)
	require.NoError(t, err, "create tool registry")

	// 6. Build executor runtime — lets builder auto-create DAO (via convsvc.NewDatly),
	//    conversation, and data services, ensuring components are registered exactly once.
	rt, err := executor.NewBuilder().
		WithAgentFinder(agentFndr).
		WithModelFinder(modelFndr).
		WithRegistry(registry).
		WithMCPManager(mcpMgr).
		WithElicitationRouter(elicrouter.New()).
		WithDefaults(&config.Defaults{
			Model:                 "openai_gpt-5.2",
			ElicitationTimeoutSec: 1,
		}).
		Build(ctx)
	require.NoError(t, err, "build runtime")
	tool.AddInternalService(rt.Registry, llmagents.New(rt.Agent, llmagents.WithConversationClient(rt.Conversation)))

	// 7. Embedded SDK client
	client, err := sdk.NewEmbeddedFromRuntime(rt)
	require.NoError(t, err, "create SDK client")
	return client
}

func prepareWorkspaceForEmbeddedE2E(t *testing.T, testdataDir, workspaceDir string) {
	t.Helper()
	dirs := []string{
		filepath.Join(workspaceDir, "agents"),
		filepath.Join(workspaceDir, "mcp"),
		filepath.Join(workspaceDir, "models"),
		filepath.Join(workspaceDir, "tools", "bundles"),
	}
	for _, dir := range dirs {
		require.NoError(t, os.MkdirAll(dir, 0o755), "mkdir %s", dir)
	}
	var copyDir func(src, dst string)
	copyDir = func(src, dst string) {
		entries, err := os.ReadDir(src)
		require.NoError(t, err, "read dir %s", src)
		for _, entry := range entries {
			srcPath := filepath.Join(src, entry.Name())
			dstPath := filepath.Join(dst, entry.Name())
			if entry.IsDir() {
				require.NoError(t, os.MkdirAll(dstPath, 0o755), "mkdir %s", dstPath)
				copyDir(srcPath, dstPath)
				continue
			}
			data, err := os.ReadFile(srcPath)
			require.NoError(t, err, "read file %s", srcPath)
			require.NoError(t, os.WriteFile(dstPath, data, 0o644), "write file %s", dstPath)
		}
	}
	copyDir(filepath.Join(testdataDir, "agents"), filepath.Join(workspaceDir, "agents"))
	copyDir(filepath.Join(testdataDir, "mcp"), filepath.Join(workspaceDir, "mcp"))
	copyDir(filepath.Join(testdataDir, "models"), filepath.Join(workspaceDir, "models"))
	copyDir(filepath.Join(testdataDir, "tools", "bundles"), filepath.Join(workspaceDir, "tools", "bundles"))
	configFile := filepath.Join(workspaceDir, "config.yaml")
	configBody := "models: []\nagents: []\n\ninternalMCP:\n  services:\n    - system/exec\n    - system/os\n"
	require.NoError(t, os.WriteFile(configFile, []byte(configBody), 0o644), "write config")
}

func TestQuerySimple(t *testing.T) {
	skipIfNoAPIKey(t)
	client := setupSDK(t)
	ctx := context.Background()

	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "simple",
		Query:   "Hi, how are you?",
		UserId:  "e2e-test",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.NotEmpty(t, out.Content, "expected non-empty response content")
	assert.NotEmpty(t, out.ConversationID, "expected conversation ID")
	fmt.Printf("[simple] content: %s\n", truncate(out.Content, 200))
}

func TestQueryWithLocalKnowledge(t *testing.T) {
	skipIfNoAPIKey(t)
	client := setupSDK(t)
	ctx := context.Background()

	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "knowledge_local",
		Query:   "What products does Viant make?",
		UserId:  "e2e-test",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.NotEmpty(t, out.Content)

	content := strings.ToLower(out.Content)
	hasProduct := strings.Contains(content, "datly") ||
		strings.Contains(content, "endly") ||
		strings.Contains(content, "agently")
	assert.True(t, hasProduct, "response should mention at least one Viant product; got: %s", truncate(out.Content, 300))
	fmt.Printf("[knowledge_local] content: %s\n", truncate(out.Content, 300))
}

func TestQueryWithSystemKnowledge(t *testing.T) {
	skipIfNoAPIKey(t)
	client := setupSDK(t)
	ctx := context.Background()

	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "knowledge_system",
		Query:   "What are the Go error handling best practices?",
		UserId:  "e2e-test",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.NotEmpty(t, out.Content)

	content := strings.ToLower(out.Content)
	hasRule := strings.Contains(content, "wrap") ||
		strings.Contains(content, "sentinel") ||
		strings.Contains(content, "errors.is") ||
		strings.Contains(content, "fmt.errorf")
	assert.True(t, hasRule, "response should reference Go error handling rules; got: %s", truncate(out.Content, 300))
	fmt.Printf("[knowledge_system] content: %s\n", truncate(out.Content, 300))
}

func TestQueryWithForcedToolUsage(t *testing.T) {
	skipIfNoAPIKey(t)
	client := setupSDK(t)
	ctx := context.Background()
	expectedUser := strings.TrimSpace(os.Getenv("USER"))
	require.NotEmpty(t, expectedUser, "USER must be set for forced tool usage e2e")

	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "tool_env_seed_user_only",
		Query:   "Please return USER only.",
		UserId:  "e2e-test",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.NotEmpty(t, out.Content)
	require.NotEmpty(t, out.ConversationID, "expected conversation ID")

	responseText := strings.TrimSpace(out.Content)
	assert.Equal(t, "USER="+expectedUser, responseText, "response should come from actual tool result")

	transcript, err := client.GetTranscript(ctx, &sdk.GetTranscriptInput{ConversationID: out.ConversationID})
	require.NoError(t, err)
	groups := transcriptExecutionGroups(transcript)
	require.NotEmpty(t, groups, "expected execution groups in transcript")
	toolCallCount := 0
	foundEnvTool := false
	for _, group := range groups {
		if group == nil {
			continue
		}
		for _, toolCall := range group.ToolCalls {
			if toolCall == nil {
				continue
			}
			toolCallCount++
			name := strings.ToLower(strings.TrimSpace(toolCall.ToolName))
			if strings.Contains(name, "system/os:getenv") || strings.Contains(name, "system_os-getenv") || strings.Contains(name, "system/os/getenv") {
				foundEnvTool = true
			}
		}
	}
	assert.Greater(t, toolCallCount, 0, "expected at least one tool call in transcript")
	assert.True(t, foundEnvTool, "expected system/os:getEnv tool call in transcript")
	fmt.Printf("[tool_forced] content: %s\n", truncate(out.Content, 300))
}

func TestQueryWithToolUsage(t *testing.T) {
	skipIfNoAPIKey(t)
	client := setupSDK(t)
	ctx := context.Background()
	expectedHome := strings.TrimSpace(os.Getenv("HOME"))
	require.NotEmpty(t, expectedHome, "HOME must be set for tool usage e2e")

	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "chatter_system_os",
		Query:   "What is the value of the HOME environment variable? Reply with the exact value only.",
		UserId:  "e2e-test",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.NotEmpty(t, out.Content)
	require.NotEmpty(t, out.ConversationID, "expected conversation ID")

	responseText := strings.TrimSpace(out.Content)
	assert.Contains(t, responseText, expectedHome, "response should include HOME value; got: %s", truncate(out.Content, 300))

	transcript, err := client.GetTranscript(ctx, &sdk.GetTranscriptInput{ConversationID: out.ConversationID})
	require.NoError(t, err)
	groups := transcriptExecutionGroups(transcript)
	require.NotEmpty(t, groups, "expected execution groups in transcript")
	toolCallCount := 0
	foundEnvTool := false
	for _, group := range groups {
		if group == nil {
			continue
		}
		for _, toolCall := range group.ToolCalls {
			if toolCall == nil {
				continue
			}
			toolCallCount++
			name := strings.ToLower(strings.TrimSpace(toolCall.ToolName))
			if strings.Contains(name, "system/os:getenv") || strings.Contains(name, "system_os-getenv") || strings.Contains(name, "system/os/getenv") {
				foundEnvTool = true
			}
		}
	}
	assert.Greater(t, toolCallCount, 0, "expected at least one tool call in transcript")
	assert.True(t, foundEnvTool, "expected system/os:getEnv tool call in transcript")
	fmt.Printf("[tool_user] content: %s\n", truncate(out.Content, 300))
}

func TestQueryMultiTurn(t *testing.T) {
	skipIfNoAPIKey(t)
	client := setupSDK(t)
	ctx := context.Background()

	// Turn 1: start a conversation
	out1, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "simple",
		Query:   "My name is Alice. Please remember that.",
		UserId:  "e2e-test",
	})
	require.NoError(t, err)
	require.NotNil(t, out1)
	assert.NotEmpty(t, out1.Content, "turn 1: expected non-empty response")
	assert.NotEmpty(t, out1.ConversationID, "turn 1: expected conversation ID")
	fmt.Printf("[multi_turn] turn 1 content: %s\n", truncate(out1.Content, 200))

	// Turn 2: continue the same conversation, reference prior context
	out2, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID:        "simple",
		ConversationID: out1.ConversationID,
		Query:          "What is my name?",
		UserId:         "e2e-test",
	})
	require.NoError(t, err)
	require.NotNil(t, out2)
	assert.NotEmpty(t, out2.Content, "turn 2: expected non-empty response")
	assert.Equal(t, out1.ConversationID, out2.ConversationID, "should use same conversation")

	content := strings.ToLower(out2.Content)
	assert.True(t, strings.Contains(content, "alice"),
		"turn 2 should remember the name Alice; got: %s", truncate(out2.Content, 300))
	fmt.Printf("[multi_turn] turn 2 content: %s\n", truncate(out2.Content, 200))
}

func TestQueryLLMSourcedElicitationFavoriteColor(t *testing.T) {
	skipIfNoAPIKey(t)
	client := setupSDK(t)
	ctx := context.Background()

	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "elicitation_favorite_color",
		Query:   "describe my favourite color in 3 sentences",
		UserId:  "e2e-test",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotEmpty(t, out.ConversationID, "expected conversation ID")
	require.NotNil(t, out.Plan, "expected plan to be present")
	require.NotNil(t, out.Plan.Elicitation, "expected model to return elicitation plan")

	elic := out.Plan.Elicitation
	assert.Contains(t, strings.ToLower(elic.Message), "favorite color", "elicitation message should request favorite color")
	assert.Contains(t, elic.RequestedSchema.Required, "favoriteColor", "required schema should include favoriteColor")
	_, hasFavoriteColor := elic.RequestedSchema.Properties["favoriteColor"]
	assert.True(t, hasFavoriteColor, "requested schema should define favoriteColor property")

	msgs, err := client.GetMessages(ctx, &sdk.GetMessagesInput{
		ConversationID: out.ConversationID,
		Roles:          []string{"assistant"},
		Types:          []string{"text"},
	})
	require.NoError(t, err)
	require.NotNil(t, msgs)

	foundElicitationMessage := false
	for _, row := range msgs.Rows {
		if row == nil || row.ElicitationId == nil || strings.TrimSpace(*row.ElicitationId) == "" {
			continue
		}
		if row.ElicitationPayloadId != nil && strings.TrimSpace(*row.ElicitationPayloadId) != "" {
			foundElicitationMessage = true
			break
		}
		if row.Content != nil && strings.Contains(strings.ToLower(*row.Content), "favorite color") {
			foundElicitationMessage = true
			break
		}
	}
	assert.True(t, foundElicitationMessage, "expected persisted assistant elicitation message with payload linkage")
}

func TestQueryOpenAIResponsesImageAttachment(t *testing.T) {
	skipIfNoAPIKey(t)
	client := setupSDK(t)
	ctx := context.Background()

	imageData := mustCreatePNG(t, color.RGBA{R: 255, A: 255})
	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID:       "simple",
		ModelOverride: "openai_gpt-5.2_responses",
		Query:         "What is the dominant color in the attached image? Answer with one word.",
		UserId:        "e2e-image",
		Attachments: []*prompt.Attachment{
			{Name: "red-dot.png", Mime: "image/png", Data: imageData},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Contains(t, strings.ToLower(out.Content), "red")

	transcript, err := client.GetTranscript(ctx, &sdk.GetTranscriptInput{ConversationID: out.ConversationID})
	require.NoError(t, err)
	require.NotNil(t, transcript)
	require.NotEmpty(t, transcript.Turns)
	require.NotEmpty(t, transcript.Turns[0].Message)
	require.NotEmpty(t, transcript.Turns[0].Message[0].Attachment)
	assert.Equal(t, "image/png", transcript.Turns[0].Message[0].Attachment[0].MimeType)
}

func TestQueryOpenAIResponsesPDFInlineAttachment(t *testing.T) {
	skipIfNoAPIKey(t)
	client := setupSDK(t)
	ctx := context.Background()

	pdfData := mustCreatePDF(t, "PDF_TEST_TOKEN_4729")
	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "pdf_inline",
		Query:   "What exact token appears in the attached PDF? Answer only with the token.",
		UserId:  "e2e-pdf-inline",
		Attachments: []*prompt.Attachment{
			{Name: "token.pdf", Mime: "application/pdf", Data: pdfData},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Contains(t, out.Content, "PDF_TEST_TOKEN_4729")
}

func TestQueryOpenAIResponsesPDFRefAttachment(t *testing.T) {
	skipIfNoAPIKey(t)
	client := setupSDK(t)
	ctx := context.Background()

	pdfData := mustCreatePDF(t, "PDF_TEST_TOKEN_4729")
	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "pdf_ref",
		Query:   "What exact token appears in the attached PDF? Answer only with the token.",
		UserId:  "e2e-pdf-ref",
		Attachments: []*prompt.Attachment{
			{Name: "token.pdf", Mime: "application/pdf", Data: pdfData},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Contains(t, out.Content, "PDF_TEST_TOKEN_4729")
}

func TestQueryOpenAIResponsesGeneratedImageOutput(t *testing.T) {
	skipIfNoAPIKey(t)
	client := setupSDK(t)
	ctx := context.Background()

	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "image_generator",
		Query:   "Generate a tiny red square PNG image and reply with only the filename.",
		UserId:  "e2e-file",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	files, err := client.ListFiles(ctx, &sdk.ListFilesInput{ConversationID: out.ConversationID})
	require.NoError(t, err)
	require.NotNil(t, files)
	require.NotEmpty(t, files.Files)
	assert.Equal(t, "generated-image.png", files.Files[0].Name)

	fileData, err := client.DownloadFile(ctx, &sdk.DownloadFileInput{
		ConversationID: out.ConversationID,
		FileID:         files.Files[0].ID,
	})
	require.NoError(t, err)
	require.NotNil(t, fileData)
	assert.True(t,
		strings.Contains(strings.ToLower(fileData.ContentType), "image/png") ||
			bytes.HasPrefix(fileData.Data, []byte{0x89, 0x50, 0x4e, 0x47}),
		"expected generated image payload; contentType=%q len=%d", fileData.ContentType, len(fileData.Data),
	)
}

func TestQueryLinkedConversationCriticReview(t *testing.T) {
	skipIfNoAPIKey(t)
	client := setupSDK(t)
	ctx := context.Background()

	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "linked_story_chatter",
		Query:   "Write a story about a dog.",
		UserId:  "e2e-linked",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "A dog named Comet found a blue ball in the park and carried it home proudly.", strings.TrimSpace(out.Content))

	transcript, err := client.GetTranscript(ctx, &sdk.GetTranscriptInput{ConversationID: out.ConversationID})
	require.NoError(t, err)
	parentGroups := transcriptExecutionGroups(transcript)
	require.NotEmpty(t, parentGroups, "expected execution groups in parent transcript")
	assert.NotNil(t, parentGroups[0].ModelCall)
	assert.NotEmpty(t, parentGroups[0].ParentMessageID)
	assert.True(t, len(parentGroups[0].ToolCalls) > 0 || len(parentGroups) > 1, "expected model-driven execution flow")
	linkedConversationID := firstLinkedConversationID(transcript)
	require.NotEmpty(t, linkedConversationID, "expected linked child conversation in transcript")

	linkedPage, err := client.ListLinkedConversations(ctx, &sdk.ListLinkedConversationsInput{
		ParentConversationID: out.ConversationID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, linkedPage.Rows)
	assert.Equal(t, linkedConversationID, linkedPage.Rows[0].ConversationID)
	assert.NotEmpty(t, linkedPage.Rows[0].Status)
	assert.Contains(t, linkedPage.Rows[0].Response, "A dog named Comet found a blue ball in the park and carried it home proudly.")

	childTranscript, err := client.GetTranscript(ctx, &sdk.GetTranscriptInput{ConversationID: linkedConversationID})
	require.NoError(t, err)
	childGroups := transcriptExecutionGroups(childTranscript)
	require.NotEmpty(t, childGroups, "expected execution groups in child transcript")
	assert.True(t, childGroups[len(childGroups)-1].FinalResponse, "expected child transcript to end with final response group")
	assert.Contains(t, childGroups[len(childGroups)-1].Content, "A dog named Comet found a blue ball in the park and carried it home proudly.")
	childText := collectTranscriptText(childTranscript)
	assert.Contains(t, childText, "A dog named Comet found a blue ball in the park and carried it home proudly.")

	limitedTranscript, err := client.GetTranscript(ctx,
		&sdk.GetTranscriptInput{ConversationID: out.ConversationID},
		sdk.WithExecutionGroupLimit(1),
		sdk.WithExecutionGroupOffset(1),
	)
	require.NoError(t, err)
	require.NotEmpty(t, limitedTranscript.Turns)
	limitedGroups := transcriptExecutionGroups(limitedTranscript)
	require.Len(t, limitedGroups, 1)
	assert.Equal(t, 1, limitedTranscript.Turns[0].ExecutionGroupsLimit)
	assert.Equal(t, 1, limitedTranscript.Turns[0].ExecutionGroupsOffset)

	offsetTranscript, err := client.GetTranscript(ctx,
		&sdk.GetTranscriptInput{ConversationID: out.ConversationID},
		sdk.WithExecutionGroupLimit(1),
		sdk.WithExecutionGroupOffset(0),
	)
	require.NoError(t, err)
	require.NotEmpty(t, offsetTranscript.Turns)
	offsetGroups := transcriptExecutionGroups(offsetTranscript)
	require.Len(t, offsetGroups, 1)
	assert.NotEqual(t, limitedGroups[0].AssistantMessageID, offsetGroups[0].AssistantMessageID)
}

func mustCreatePNG(t *testing.T, fill color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.SetRGBA(0, 0, fill)
	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	require.NoError(t, err)
	return buf.Bytes()
}

func mustCreatePDF(t *testing.T, text string) []byte {
	t.Helper()
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetCompression(false)
	pdf.AddPage()
	pdf.SetFont("Helvetica", "", 16)
	pdf.Text(20, 30, text)
	var buf bytes.Buffer
	err := pdf.Output(&buf)
	require.NoError(t, err)
	return buf.Bytes()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func firstLinkedConversationID(transcript *sdk.TranscriptOutput) string {
	if transcript == nil {
		return ""
	}
	for _, turn := range transcript.Turns {
		if turn == nil {
			continue
		}
		for _, message := range turn.Message {
			if message == nil || message.LinkedConversationId == nil {
				continue
			}
			if value := strings.TrimSpace(*message.LinkedConversationId); value != "" {
				return value
			}
		}
	}
	return ""
}

func collectTranscriptText(transcript *sdk.TranscriptOutput) string {
	var parts []string
	if transcript == nil {
		return ""
	}
	for _, turn := range transcript.Turns {
		if turn == nil {
			continue
		}
		for _, message := range turn.Message {
			if message == nil || message.Content == nil {
				continue
			}
			if text := strings.TrimSpace(*message.Content); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func transcriptExecutionGroups(transcript *sdk.TranscriptOutput) []*sdk.ExecutionGroup {
	var groups []*sdk.ExecutionGroup
	if transcript == nil {
		return nil
	}
	for _, turn := range transcript.Turns {
		if turn == nil || len(turn.ExecutionGroups) == 0 {
			continue
		}
		groups = append(groups, turn.ExecutionGroups...)
	}
	return groups
}

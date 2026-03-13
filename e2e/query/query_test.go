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

	// 7. Embedded SDK client
	client, err := sdk.NewEmbeddedFromRuntime(rt)
	require.NoError(t, err, "create SDK client")
	return client
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

func TestQueryWithToolUsage(t *testing.T) {
	skipIfNoAPIKey(t)
	client := setupSDK(t)
	ctx := context.Background()

	out, err := client.Query(ctx, &agentsvc.QueryInput{
		AgentID: "tool_user",
		Query:   "What is the value of the OPENAI_API_KEY environment variable? Just tell me if it exists or not and the first 10 characters.",
		UserId:  "e2e-test",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.NotEmpty(t, out.Content)

	content := strings.ToLower(out.Content)
	hasEnvInfo := strings.Contains(content, "sk-") ||
		strings.Contains(content, "openai") ||
		strings.Contains(content, "api") ||
		strings.Contains(content, "environment") ||
		strings.Contains(content, "variable")
	assert.True(t, hasEnvInfo, "response should reference the env variable; got: %s", truncate(out.Content, 300))
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
		if row.Content != nil && strings.Contains(strings.ToLower(*row.Content), "favoritecolor") {
			foundElicitationMessage = true
			break
		}
	}
	assert.True(t, foundElicitationMessage, "expected persisted assistant message with elicitation_id and favoriteColor schema")
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

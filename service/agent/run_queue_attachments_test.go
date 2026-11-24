package agent

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	authctx "github.com/viant/agently-core/internal/auth"
	memconv "github.com/viant/agently-core/internal/service/conversation/memory"
	generatedfilemodel "github.com/viant/agently-core/model/generatedfile"
	turnmodel "github.com/viant/agently-core/model/turn"
	agentmodel "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
)

type attachmentQueueData struct{ data.Service }

func (*attachmentQueueData) GetActiveTurn(context.Context, *turnmodel.ActiveTurnsInput, ...data.Option) (*turnmodel.ActiveTurnsView, error) {
	return &turnmodel.ActiveTurnsView{Id: "active"}, nil
}

func (*attachmentQueueData) CountQueuedTurns(context.Context, *turnmodel.QueuedTotalInput, ...data.Option) (int, error) {
	return 0, nil
}

type attachmentQueueClient struct {
	*memconv.Client
	onQueued func(context.Context)
}

func (c *attachmentQueueClient) PatchTurn(ctx context.Context, turn *apiconv.MutableTurn) error {
	if turn.Status == "queued" && c.onQueued != nil {
		c.onQueued(ctx)
	}
	return c.Client.PatchTurn(ctx, turn)
}

func TestQueuedUploadPersistsNativeCarrierBeforeQueueAdmission(t *testing.T) {
	t.Setenv(scratchpadsvc.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"})
	body := []byte(`{"example":"independent synthetic attachment"}`)
	artifact, err := scratchpadsvc.New().PublishArtifact(ctx, "setup", "setup.json", "application/json", "", bytes.NewReader(body))
	require.NoError(t, err)
	client := &attachmentQueueClient{Client: memconv.New()}
	conversation := apiconv.NewConversation()
	conversation.SetId("conversation")
	require.NoError(t, client.PatchConversations(ctx, conversation))
	payload := apiconv.NewPayload()
	payload.SetId("payload")
	payload.SetKind("attachment")
	payload.SetMimeType("application/json")
	payload.SetInlineBody(body)
	payload.SetURI(artifact.URI)
	require.NoError(t, client.PatchPayload(ctx, payload))
	file := &generatedfilemodel.GeneratedFile{}
	file.SetID("file")
	file.SetConversationID("conversation")
	file.SetPayloadID("payload")
	file.SetFilename("setup.json")
	file.SetMimeType("application/json")
	require.NoError(t, client.PatchGeneratedFile(ctx, file))
	service := &Service{conversation: client, dataService: &attachmentQueueData{}, llm: core.New(continuationFinder{}, nil, client)}
	queued := false
	client.onQueued = func(ctx context.Context) {
		queued = true
		stored, err := client.GetConversation(ctx, "conversation")
		require.NoError(t, err)
		var carriers int
		for _, turn := range stored.GetTranscript() {
			if turn.Id != "queued" {
				continue
			}
			for _, message := range turn.Message {
				if message.AttachmentPayloadId != nil {
					carriers++
				}
			}
		}
		require.Equal(t, 1, carriers, "a queue reader must never select a turn before its file carrier is durable")
	}
	input := &QueryInput{ConversationID: "conversation", MessageID: "queued", UserId: "owner", Query: "Review the attached setup", Agent: &agentmodel.Agent{}, Attachments: []*binding.Attachment{{URI: "/v1/files/file?conversationId=conversation"}}}
	admitted, err := service.tryQueueTurn(ctx, input)
	require.NoError(t, err)
	require.True(t, admitted)
	require.True(t, queued)
	stored, err := client.GetConversation(ctx, "conversation")
	require.NoError(t, err)
	history, err := service.buildHistory(requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: "conversation", TurnID: "queued"}), stored.GetTranscript())
	require.NoError(t, err)
	messages := history.LLMMessages()
	require.Len(t, messages, 1)
	require.Contains(t, messages[0].Content, artifact.URI)
	require.NotContains(t, messages[0].Content, string(body))
}

func TestConcurrentQueuedUploadsRespectTheNativeCumulativeCap(t *testing.T) {
	ctx := context.Background()
	client := memconv.New()
	conversation := apiconv.NewConversation()
	conversation.SetId("conversation")
	require.NoError(t, client.PatchConversations(ctx, conversation))
	service := &Service{conversation: client, dataService: &attachmentQueueData{}, llm: core.New(continuationFinder{}, nil, client)}
	results := make(chan error, 2)
	var pending sync.WaitGroup
	for _, id := range []string{"one", "two"} {
		pending.Add(1)
		go func(id string) {
			defer pending.Done()
			input := &QueryInput{ConversationID: "conversation", MessageID: id, Query: "Use file", Agent: &agentmodel.Agent{Attachment: &agentmodel.Attachment{LimitBytes: 2}}, Attachments: []*binding.Attachment{{Name: "data.bin", Data: []byte("ok")}}}
			_, err := service.tryQueueTurn(ctx, input)
			results <- err
		}(id)
	}
	pending.Wait()
	close(results)
	var admitted, rejected int
	for err := range results {
		if err == nil {
			admitted++
		} else if strings.Contains(err.Error(), "attachments exceed agent cap") {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	require.Equal(t, 1, admitted)
	require.Equal(t, 1, rejected)
	stored, err := client.GetConversation(ctx, "conversation")
	require.NoError(t, err)
	statuses := map[string]int{}
	for _, turn := range stored.GetTranscript() {
		statuses[turn.Status]++
	}
	require.Equal(t, map[string]int{"queued": 1, "failed": 1}, statuses)
}

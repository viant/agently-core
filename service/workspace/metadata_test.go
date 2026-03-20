package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/app/executor/config"
	ws "github.com/viant/agently-core/workspace"
)

type metadataTestStore struct {
	items map[string]map[string][]byte
}

func (s *metadataTestStore) Root() string { return "/tmp/test" }
func (s *metadataTestStore) List(_ context.Context, kind string) ([]string, error) {
	var result []string
	for name := range s.items[kind] {
		result = append(result, name)
	}
	return result, nil
}
func (s *metadataTestStore) Load(_ context.Context, kind, name string) ([]byte, error) {
	return s.items[kind][name], nil
}
func (s *metadataTestStore) Save(context.Context, string, string, []byte) error { return nil }
func (s *metadataTestStore) Delete(context.Context, string, string) error       { return nil }
func (s *metadataTestStore) Exists(_ context.Context, kind, name string) (bool, error) {
	_, ok := s.items[kind][name]
	return ok, nil
}
func (s *metadataTestStore) Entries(_ context.Context, kind string) ([]ws.Entry, error) {
	var result []ws.Entry
	for name, data := range s.items[kind] {
		result = append(result, ws.Entry{Kind: kind, Name: name, Data: data, UpdatedAt: time.Now()})
	}
	return result, nil
}

func TestMetadataHandler_StarterTasks(t *testing.T) {
	handler := NewMetadataHandler(&config.Defaults{
		Agent:    "chatter",
		Model:    "openai_gpt4o_mini",
		Embedder: "openai_text",
		ToolAutoSelection: config.ToolAutoSelectionDefaults{
			Enabled: true,
		},
	}, nil, "test-version").SetStarterTasks([]StarterTask{
		{
			ID:          "analyze-repo",
			Title:       "Analyze this repo",
			Prompt:      "Analyze this repository.",
			Description: "Architecture summary and next steps.",
			Icon:        "tree-structure",
		},
	})

	mux := http.NewServeMux()
	handler.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/v1/workspace/metadata", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response MetadataResponse
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.NotEmpty(t, response.WorkspaceRoot)
	assert.Equal(t, "chatter", response.DefaultAgent)
	assert.Equal(t, "openai_gpt4o_mini", response.DefaultModel)
	assert.Equal(t, "openai_text", response.DefaultEmbedder)
	if assert.NotNil(t, response.Defaults) {
		assert.Equal(t, "chatter", response.Defaults.Agent)
		assert.Equal(t, "openai_gpt4o_mini", response.Defaults.Model)
		assert.Equal(t, "openai_text", response.Defaults.Embedder)
		assert.True(t, response.Defaults.AutoSelectTools)
	}
	assert.True(t, response.Capabilities.AgentAutoSelection)
	assert.False(t, response.Capabilities.ModelAutoSelection)
	assert.True(t, response.Capabilities.ToolAutoSelection)
	assert.True(t, response.Capabilities.CompactConversation)
	assert.True(t, response.Capabilities.PruneConversation)
	assert.True(t, response.Capabilities.AnonymousSession)
	assert.True(t, response.Capabilities.MessageCursor)
	assert.True(t, response.Capabilities.StructuredElicitation)
	assert.True(t, response.Capabilities.TurnStartedEvent)
	if assert.Len(t, response.StarterTasks, 1) {
		assert.Equal(t, "analyze-repo", response.StarterTasks[0].ID)
		assert.Equal(t, "Analyze this repo", response.StarterTasks[0].Title)
		assert.Equal(t, "tree-structure", response.StarterTasks[0].Icon)
	}
}

func TestMetadataHandler_DescriptorInfos(t *testing.T) {
	store := &metadataTestStore{
		items: map[string]map[string][]byte{
			ws.KindAgent: {
				"coder":             []byte("id: coder\nname: Coder\nmodelRef: openai_gpt-5.2\n"),
				"chat_helper_agent": []byte("id: chat-helper\nname: Chat Helper\nmodelRef: vertexai_gemini-2.5-flash\n"),
			},
			ws.KindModel: {
				"openai_gpt-5_2":           []byte("id: openai_gpt-5.2\nname: GPT-5.2\n"),
				"vertexai_gemini_flash2_5": []byte("id: vertexai_gemini-2.5-flash\nname: Gemini 2.5 Flash\n"),
			},
		},
	}
	handler := NewMetadataHandler(&config.Defaults{}, store, "test-version")

	mux := http.NewServeMux()
	handler.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/v1/workspace/metadata", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response MetadataResponse
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.NotEmpty(t, response.WorkspaceRoot)
	if assert.Len(t, response.AgentInfos, 2) {
		agents := map[string]AgentInfo{}
		for _, item := range response.AgentInfos {
			agents[item.ID] = item
		}
		assert.Equal(t, "Coder", agents["coder"].Name)
		assert.Equal(t, "openai_gpt-5.2", agents["coder"].ModelRef)
		assert.Equal(t, "Chat Helper", agents["chat-helper"].Name)
		assert.Equal(t, "vertexai_gemini-2.5-flash", agents["chat-helper"].ModelRef)
	}
	assert.ElementsMatch(t, []string{"coder", "chat-helper"}, response.Agents)
	if assert.Len(t, response.ModelInfos, 2) {
		models := map[string]string{}
		for _, item := range response.ModelInfos {
			models[item.ID] = item.Name
		}
		assert.Equal(t, "GPT-5.2", models["openai_gpt-5.2"])
		assert.Equal(t, "Gemini 2.5 Flash", models["vertexai_gemini-2.5-flash"])
	}
	assert.ElementsMatch(t, []string{"openai_gpt-5.2", "vertexai_gemini-2.5-flash"}, response.Models)
}

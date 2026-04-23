package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/app/executor/config"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
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
	prevRoot := ws.Root()
	tempRoot := t.TempDir()
	ws.SetRoot(tempRoot)
	defer ws.SetRoot(prevRoot)

	store := &metadataTestStore{
		items: map[string]map[string][]byte{
			ws.KindAgent: {
				"coder": []byte("id: coder\nname: Coder\nmodelRef: openai_gpt-5.2\ntool:\n  bundles:\n    - system/exec\n    - system/patch\nstarterTasks:\n  - id: analyze-repo\n    title: Analyze this repo\n    prompt: Analyze this repository.\n    description: Architecture summary and next steps.\n    icon: tree-structure\n"),
			},
		},
	}
	handler := NewMetadataHandler(&config.Defaults{
		Agent:    "chatter",
		Model:    "openai_gpt4o_mini",
		Embedder: "openai_text",
		ToolAutoSelection: config.ToolAutoSelectionDefaults{
			Enabled: true,
		},
	}, store, "test-version")

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
	assert.Equal(t, "0.0.0", response.WorkspaceVersion)
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
	if assert.Len(t, response.AgentInfos, 1) {
		assert.Equal(t, "coder", response.AgentInfos[0].ID)
		assert.ElementsMatch(t, []string{"system/exec", "system/patch"}, response.AgentInfos[0].Tools)
		if assert.Len(t, response.AgentInfos[0].StarterTasks, 1) {
			assert.Equal(t, "analyze-repo", response.AgentInfos[0].StarterTasks[0].ID)
			assert.Equal(t, "Analyze this repo", response.AgentInfos[0].StarterTasks[0].Title)
			assert.Equal(t, "tree-structure", response.AgentInfos[0].StarterTasks[0].Icon)
		}
	}
}

func TestMetadataHandler_WorkspaceVersionFromRootFile(t *testing.T) {
	prevRoot := ws.Root()
	tempRoot := t.TempDir()
	ws.SetRoot(tempRoot)
	defer ws.SetRoot(prevRoot)

	err := os.WriteFile(filepath.Join(tempRoot, "Version"), []byte("1.2.3\n"), 0o644)
	assert.NoError(t, err)

	handler := NewMetadataHandler(&config.Defaults{}, &metadataTestStore{items: map[string]map[string][]byte{}}, "test-version")
	mux := http.NewServeMux()
	handler.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/v1/workspace/metadata", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response MetadataResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, tempRoot, response.WorkspaceRoot)
	assert.Equal(t, "1.2.3", response.WorkspaceVersion)
	assert.Equal(t, "test-version", response.Version)
}

func TestMetadataHandler_DescriptorInfos(t *testing.T) {
	store := &metadataTestStore{
		items: map[string]map[string][]byte{
			ws.KindAgent: {
				"coder":             []byte("id: coder\nname: Coder\nmodelRef: openai_gpt-5.2\ntool:\n  bundles:\n    - system/exec\n  items:\n    - name: system/patch\n"),
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
		assert.ElementsMatch(t, []string{"system/exec", "system/patch"}, agents["coder"].Tools)
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

func TestMetadataHandler_IncludesInternalFlagForAgents(t *testing.T) {
	store := &metadataTestStore{
		items: map[string]map[string][]byte{
			ws.KindAgent: {
				"public_agent":   []byte("id: public-agent\nname: Public Agent\n"),
				"internal_agent": []byte("id: internal-agent\nname: Internal Agent\ninternal: true\n"),
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
	if assert.Len(t, response.AgentInfos, 2) {
		agents := map[string]AgentInfo{}
		for _, item := range response.AgentInfos {
			agents[item.ID] = item
		}
		assert.False(t, agents["public-agent"].Internal)
		assert.True(t, agents["internal-agent"].Internal)
	}
	assert.ElementsMatch(t, []string{"public-agent", "internal-agent"}, response.Agents)
}

func TestMetadataHandler_SortsAgentAndModelInfosByLabel(t *testing.T) {
	store := &metadataTestStore{
		items: map[string]map[string][]byte{
			ws.KindAgent: {
				"zebra_agent": []byte("id: zebra\nname: Zebra\nmodelRef: openai_gpt-5.2\n"),
				"alpha_agent": []byte("id: alpha\nname: Alpha\nmodelRef: openai_gpt-5-mini\n"),
			},
			ws.KindModel: {
				"z_model": []byte("id: z-model\nname: Zebra Model\n"),
				"a_model": []byte("id: a-model\nname: Alpha Model\n"),
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
	if assert.Len(t, response.AgentInfos, 2) {
		assert.Equal(t, "alpha", response.AgentInfos[0].ID)
		assert.Equal(t, "zebra", response.AgentInfos[1].ID)
	}
	assert.Equal(t, []string{"alpha", "zebra"}, response.Agents)
	if assert.Len(t, response.ModelInfos, 2) {
		assert.Equal(t, "a-model", response.ModelInfos[0].ID)
		assert.Equal(t, "z-model", response.ModelInfos[1].ID)
	}
	assert.Equal(t, []string{"a-model", "z-model"}, response.Models)
}

func TestAgentToolDefaults(t *testing.T) {
	testCases := []struct {
		name     string
		agent    *agentmdl.Agent
		raw      map[string]interface{}
		expected []string
	}{
		{
			name: "bundles from decoded agent config",
			agent: &agentmdl.Agent{
				Tool: agentmdl.Tool{
					Bundles: []string{"system/exec", "system/patch"},
				},
			},
			expected: []string{"system/exec", "system/patch"},
		},
		{
			name: "explicit tool item names from decoded agent config",
			agent: &agentmdl.Agent{
				Tool: agentmdl.Tool{
					Items: []*llm.Tool{
						{Name: "system/exec"},
						{Definition: llm.ToolDefinition{Name: "system/patch"}},
					},
				},
			},
			expected: []string{"system/exec", "system/patch"},
		},
		{
			name: "legacy raw pattern fallback",
			raw: map[string]interface{}{
				"tool": map[string]interface{}{
					"items": []interface{}{
						map[string]interface{}{"pattern": "system/exec"},
						map[string]interface{}{"pattern": "system/patch"},
					},
				},
			},
			expected: []string{"system/exec", "system/patch"},
		},
		{
			name: "dedupes mixed bundle and item sources",
			agent: &agentmdl.Agent{
				Tool: agentmdl.Tool{
					Bundles: []string{"system/exec"},
					Items: []*llm.Tool{
						{Name: "system/patch"},
					},
				},
			},
			raw: map[string]interface{}{
				"tool": map[string]interface{}{
					"bundles": []interface{}{"system/exec"},
					"items": []interface{}{
						map[string]interface{}{"name": "system/patch"},
						map[string]interface{}{"pattern": "system/exec"},
					},
				},
			},
			expected: []string{"system/exec", "system/patch"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := agentToolDefaults(testCase.agent, testCase.raw)
			assert.ElementsMatch(t, testCase.expected, actual)
		})
	}
}

package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
)

func TestNativeImageDetailPreservedForInlineAndUploadedResponses(t *testing.T) {
	for _, mode := range []string{"inline", "upload"} {
		t.Run(mode, func(t *testing.T) {
			assets := &fakeOpenAIAssetManager{fileID: "fixture-image"}
			client := &Client{APIKey: "fixture", storageMgrAPIKey: "fixture", storageMgr: assets}
			item := llm.NewBinaryContent([]byte{0}, "image/png", "screenshot.png")
			item.Metadata = map[string]interface{}{"nativePresentation": true, "detail": "high"}
			request, err := client.ToRequest(&llm.GenerateRequest{Messages: []llm.Message{{Role: llm.RoleUser, Items: []llm.ContentItem{item}}}, Options: &llm.Options{Model: "gpt-6.1-sol", Metadata: map[string]interface{}{"attachMode": mode}}})
			require.NoError(t, err)
			parts := ToResponsesPayload(request).Input[0].Content
			require.Len(t, parts, 1)
			require.Equal(t, "input_image", parts[0].Type)
			require.Equal(t, "high", parts[0].Detail)
			if mode == "upload" {
				require.Equal(t, "fixture-image", parts[0].FileID)
			}
		})
	}
}

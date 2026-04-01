package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider/base"
)

type capabilityModelStub struct {
	supported map[string]bool
}

func (m *capabilityModelStub) Generate(_ context.Context, _ *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	return nil, nil
}

func (m *capabilityModelStub) Implements(feature string) bool {
	return m.supported[feature]
}

func TestValidateModelNativeCapabilities_ModelArtifactGeneration(t *testing.T) {
	t.Run("supported model keeps capability", func(t *testing.T) {
		opts := &llm.Options{
			Metadata: map[string]interface{}{
				"modelArtifactGeneration": true,
			},
		}
		normalizeModelNativeCapabilities(opts, &capabilityModelStub{
			supported: map[string]bool{
				base.SupportsModelArtifactGeneration: true,
			},
		}, "openai_gpt-5.2_responses")
		got, ok := opts.Metadata["modelArtifactGeneration"].(bool)
		assert.True(t, ok)
		assert.True(t, got)
	})

	t.Run("unsupported model strips capability", func(t *testing.T) {
		opts := &llm.Options{
			Metadata: map[string]interface{}{
				"modelArtifactGeneration": true,
			},
		}
		normalizeModelNativeCapabilities(opts, &capabilityModelStub{
			supported: map[string]bool{},
		}, "vertexai_gemini_3_0_pro")
		_, ok := opts.Metadata["modelArtifactGeneration"]
		assert.False(t, ok)
	})
}

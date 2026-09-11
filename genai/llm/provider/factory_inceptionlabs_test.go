package provider

import (
	"context"
	"testing"

	"github.com/viant/agently-core/genai/llm/provider/inceptionlabs"
)

func TestInceptionLabsEnvironmentKeyOverridesSecretFallback(t *testing.T) {
	t.Setenv("INCEPTIONLABS_API_KEY", "env-mercury-key")

	model, err := (&Factory{}).CreateModel(context.Background(), &Options{
		Provider:  ProviderInceptionLabs,
		Model:     "mercury-2",
		APIKeyURL: "/does/not/exist.enc|blowfish://default",
	})
	if err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}
	client, ok := model.(*inceptionlabs.Client)
	if !ok {
		t.Fatalf("CreateModel() type = %T, want *inceptionlabs.Client", model)
	}
	if client.APIKey != "env-mercury-key" {
		t.Fatalf("API key did not come from environment")
	}
}

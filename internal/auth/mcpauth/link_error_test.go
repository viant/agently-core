package mcpauth

import (
	"errors"
	"fmt"
	"testing"

	authcfg "github.com/viant/mcp/client/auth/config"
)

func upstreamError() *authcfg.OAuthLinkRequiredError {
	return &authcfg.OAuthLinkRequiredError{
		ServerName:  "sample-mcp",
		ProviderRef: "sample-provider",
		Issuer:      "https://idp-sample.example.com",
		Resource:    "https://mcp6.example.com/mcp",
		Scopes:      []string{"plan:create", "plan:edit", "plan:read"},
		MetadataURL: "https://mcp6.example.com/.well-known/oauth-protected-resource",
		Cause:       errors.New("no stored credential"),
	}
}

func TestFromErrorMapsUpstreamPreservingBindingFields(t *testing.T) {
	wrapped := fmt.Errorf("call tool: %w", upstreamError())
	typed, ok := FromError(wrapped)
	if !ok {
		t.Fatalf("expected mapping for wrapped upstream error")
	}
	if typed.ServerName != "sample-mcp" || typed.ProviderRef != "sample-provider" {
		t.Fatalf("identity fields lost: %+v", typed)
	}
	if typed.Issuer == "" || typed.Resource == "" || typed.MetadataURL == "" {
		t.Fatalf("issuer/resource/metadata URL must survive mapping for learned binding: %+v", typed)
	}
	if len(typed.Scopes) != 3 {
		t.Fatalf("scopes lost: %v", typed.Scopes)
	}
}

func TestFromErrorPassesThroughUnrelated(t *testing.T) {
	if _, ok := FromError(errors.New("plain failure")); ok {
		t.Fatalf("unrelated errors must not map to link-required")
	}
	if err := WrapError(errors.New("plain failure")); err == nil || IsLinkRequired(err) {
		t.Fatalf("WrapError must pass unrelated errors through unchanged")
	}
	if _, ok := FromError(nil); ok {
		t.Fatalf("nil must not map")
	}
}

func TestAPIErrorShape(t *testing.T) {
	typed, _ := FromError(upstreamError())
	api := typed.APIError()
	if api["code"] != APICode {
		t.Fatalf("code = %v", api["code"])
	}
	if api["server"] != "sample-mcp" || api["provider"] != "sample-provider" {
		t.Fatalf("api identity = %v", api)
	}
	if api["connectURL"] != "/v1/api/auth/mcp/sample-mcp/initiate" {
		t.Fatalf("connectURL = %v", api["connectURL"])
	}
	for _, forbidden := range []string{"token", "secret", "authorizationURL", "code_verifier"} {
		if _, present := api[forbidden]; present {
			t.Fatalf("API error must not carry %q", forbidden)
		}
	}
}

func TestWrapErrorProducesStableTypedError(t *testing.T) {
	err := WrapError(fmt.Errorf("discover: %w", upstreamError()))
	var typed *LinkRequiredError
	if !errors.As(err, &typed) {
		t.Fatalf("expected Agently-typed link error, got %T", err)
	}
	if !IsLinkRequired(err) {
		t.Fatalf("IsLinkRequired must recognize the typed error")
	}
	// Idempotent: wrapping an already-typed error keeps it.
	if again := WrapError(err); again != err {
		if _, ok := FromError(again); !ok {
			t.Fatalf("re-wrap lost the typed error")
		}
	}
}

func TestMappedLinkRequiredRetainsCanonicalClassificationAndErrorChain(t *testing.T) {
	original := upstreamError()
	mapped := WrapError(fmt.Errorf("delegated preflight: %w", original))
	var canonical *authcfg.OAuthLinkRequiredError
	if !errors.As(mapped, &canonical) || canonical != original {
		t.Fatalf("mapped error lost canonical OAuth classification: %T: %v", mapped, mapped)
	}
	if !errors.Is(mapped, original.Cause) {
		t.Fatal("mapped error lost original cause")
	}
	if again := WrapError(mapped); again != mapped {
		t.Fatal("mapping must remain idempotent")
	}
	sibling := errors.New("independent integrity failure")
	for _, input := range []error{errors.Join(original, sibling), errors.Join(mapped, sibling)} {
		converted := WrapError(input)
		if !errors.Is(converted, sibling) {
			t.Fatalf("mapping discarded joined non-link failure: %v", converted)
		}
		if !authcfg.IsLinkRequired(converted) {
			t.Fatal("joined chain lost canonical link error")
		}
	}
}

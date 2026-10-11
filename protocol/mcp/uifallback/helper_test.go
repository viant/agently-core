package uifallback

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp-ui/capabilities"
	"github.com/viant/mcp-ui/meta"
)

func TestGenericResourceNegotiation(t *testing.T) {
	const uri = "ui://weather-dashboard"
	const html = "<!doctype html><html><body>weather</body></html>"
	mime := capabilities.ResourceMimeType
	for _, tc := range []struct {
		name     string
		mimes    []string
		fallback string
		want     bool
	}{
		{"absent", nil, meta.FallbackEmbedded, true},
		{"wrong MIME", []string{"text/html"}, meta.FallbackEmbedded, true},
		{"negotiated", []string{mime}, meta.FallbackEmbedded, false},
		{"no opt in", nil, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caps := &schema.ClientCapabilities{}
			capabilities.SetClientCapability(caps, capabilities.Capability{MimeTypes: tc.mimes})
			calls := 0
			read := func(_ context.Context, got string) (*schema.ReadResourceResult, error) {
				calls++
				if got != uri {
					t.Fatalf("URI changed: %q", got)
				}
				return &schema.ReadResourceResult{Contents: []schema.ReadResourceResultContentsElem{
					{Uri: "ui://other", Text: "wrong"},
					{Uri: uri, MimeType: &mime, Blob: base64.StdEncoding.EncodeToString([]byte(html))},
				}}, nil
			}
			result, embedded, err := EmbeddedCallToolContent(context.Background(), caps, meta.ToolUI{ResourceUri: uri, Fallback: tc.fallback}, map[string]interface{}{"resourceUri": uri}, read)
			if err != nil || embedded != tc.want {
				t.Fatalf("embedded=%v error=%v", embedded, err)
			}
			if tc.want {
				if calls != 1 || result.Resource.Uri != uri || result.Resource.Text != html {
					t.Fatalf("incorrect resource: %#v", result)
				}
			} else if calls != 0 {
				t.Fatal("unexpected resource read")
			}
		})
	}
}

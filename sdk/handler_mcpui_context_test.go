package sdk

import (
	"context"
	"errors"
	"github.com/viant/mcp-protocol/schema"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPUIOrdinaryPostRequiresValidatedWindowContext(t *testing.T) {
	for _, test := range []struct {
		name, body string
		deny       bool
		want       int
	}{
		{"accepted", `{"uri":"ui://owner/app","resource":{"uri":"window://authoring/App"},"resourceTarget":{"selectionToken":"owned-proof"}}`, false, 200},
		{"denied", `{"uri":"ui://owner/app","resource":{"uri":"window://authoring/App"},"resourceTarget":{"selectionToken":"foreign-proof"}}`, true, 403},
		{"missing-context", `{"uri":"ui://owner/app"}`, false, 400},
		{"unknown-field", `{"uri":"ui://owner/app","resource":{},"resourceTarget":{},"_meta":{"business":"spoof"}}`, false, 400},
		{"trailing", `{"uri":"ui://owner/app","resource":{},"resourceTarget":{}} {}`, false, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			reads := 0
			key := struct{}{}
			reader := func(ctx context.Context, uri string) (*schema.ReadResourceResult, error) {
				reads++
				if ctx.Value(key) != true {
					return nil, errors.New("unvalidated context")
				}
				mime := "text/html;profile=mcp-app"
				return &schema.ReadResourceResult{ResultType: schema.ResultTypeComplete, CacheScope: schema.ReadResourceResultCacheScopePrivate, Contents: []schema.ReadResourceResultContentsElem{{Uri: uri, MimeType: &mime, Text: "<main>original</main>"}}}, nil
			}
			bind := func(ctx context.Context, in *MCPUIResourceReadInput) (context.Context, error) {
				if test.deny {
					return nil, errors.New("selected window denied")
				}
				if in.Resource.URI != "window://authoring/App" || in.ResourceTarget.SelectionToken != "owned-proof" {
					return nil, errors.New("wrong opened window")
				}
				return context.WithValue(ctx, key, true), nil
			}
			handler := NewHandler(nil, WithMCPUIResourceReader(reader), WithMCPUIResourceContextBinder(bind))
			request := httptest.NewRequest(http.MethodPost, "/v1/api/mcp-ui/resources/read", strings.NewReader(test.body))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.want, response.Body.String())
			}
			wantReads := 0
			if test.want == 200 {
				wantReads = 1
			}
			if reads != wantReads {
				t.Fatalf("unvalidated reader calls=%d", reads)
			}
		})
	}
}

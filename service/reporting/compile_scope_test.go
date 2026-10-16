package reporting

import (
	"context"
	"encoding/json"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/reporting/catalog"
	"testing"
	"time"
)

type compileMetadataMarker struct{}

func TestCanonicalCompilerBuffersMetadataScopeFinalDenial(t *testing.T) {
	uri, _ := identity.ParseResourceURI("report://scope/compiler")
	raw := json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"source"},"reportSpec":{"title":"trusted"}}`)
	resolver := &identity.ResourceResolver{Source: &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) { return raw, nil }}, Policy: reportPolicyFunc(func(_ context.Context, _ identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
		return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: "actor", ValidUntil: time.Now().Add(time.Minute)}, nil
	})}
	begins, finishes, compiled := 0, 0, 0
	deny := false
	catalog := &catalog.ReportCatalogService{BeginMetadataRead: func(ctx context.Context) (context.Context, func() error, error) {
		begins++
		return context.WithValue(ctx, compileMetadataMarker{}, true), func() error {
			finishes++
			if deny {
				return identity.ErrResourceDenied
			}
			return nil
		}, nil
	}}
	service := New(Options{Store: NewMemoryStore(), ReportCatalog: catalog, ResourceResolver: func(context.Context, string) (*identity.ResourceResolver, error) { return resolver, nil }, Compiler: reportCompilerFunc(func(ctx context.Context, in *CompileRequest) (*CompileResult, error) {
		compiled++
		if ctx.Value(compileMetadataMarker{}) != true {
			t.Fatal("compiler metadata scope missing")
		}
		return &CompileResult{ReportSpec: in.Document}, nil
	})})
	request := &CompileRequest{Resource: &identity.ResourceRef{URI: uri.String()}}
	if out, err := service.Compile(context.Background(), request); err != nil || out == nil || begins != 1 || finishes != 1 {
		t.Fatal("configured metadata phase did not finalize")
	}
	deny = true
	if out, err := service.Compile(context.Background(), request); err == nil || out != nil || begins != 2 || finishes != 2 || compiled != 2 {
		t.Fatal("compiler released output after final authority denial")
	}
}

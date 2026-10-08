package reporting

import (
	"context"
	"encoding/json"
	"github.com/viant/forge/backend/reporting/registry"
	"os"
	"strings"
	"testing"
	"time"

	identity "github.com/viant/agently-core/protocol/resource"
)

type reportPolicyFunc func(context.Context, identity.ResourceRef, []identity.ResourceCandidate) (identity.ResourceDecision, error)

func (f reportPolicyFunc) SelectRevision(ctx context.Context, ref identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	return f(ctx, ref, c)
}

type reportCompilerFunc func(context.Context, *CompileRequest) (*CompileResult, error)

func (f reportCompilerFunc) Compile(ctx context.Context, r *CompileRequest) (*CompileResult, error) {
	return f(ctx, r)
}

func TestReportingCompileToolUsesPinnedStoredDefinition(t *testing.T) {
	uri, _ := identity.ParseResourceURI("report://analytics/sales")
	content := json.RawMessage(`{"schemaVersion":1,"reportDocument":{"title":"approved"},"reportSpec":{"title":"approved","datasets":[]}}`)
	allowed := true
	source := &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) { return append(json.RawMessage(nil), content...), nil }}
	authority := reportPolicyFunc(func(_ context.Context, _ identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
		if !allowed {
			return identity.ResourceDecision{}, identity.ErrResourceDenied
		}
		return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: "verified-account", ValidUntil: time.Now().Add(time.Minute)}, nil
	})
	resolver := &identity.ResourceResolver{Source: source, Policy: authority}
	compiled := 0
	changeDuringCompile := false
	compiler := reportCompilerFunc(func(_ context.Context, in *CompileRequest) (*CompileResult, error) {
		compiled++
		if in.SourceKind != SourceKindReportSpec || in.Resource == nil || in.Resource.Revision != "working" || !strings.Contains(string(in.Document), "approved") || strings.Contains(string(in.Document), "forged") {
			t.Fatalf("compiler input lost trusted resource: %+v", in)
		}
		if changeDuringCompile {
			content = json.RawMessage(`{"schemaVersion":1,"reportDocument":{},"reportSpec":{"title":"changed"}}`)
		}
		return &CompileResult{ReportSpec: in.Document}, nil
	})
	service := New(Options{Store: NewMemoryStore(), Compiler: compiler, ResourceResolver: func(_ context.Context, operation string) (*identity.ResourceResolver, error) {
		if operation != "report.compile" {
			t.Fatal(operation)
		}
		return resolver, nil
	}})
	method, err := service.Method("compile")
	if err != nil {
		t.Fatal(err)
	}
	input := &CompileRequest{Resource: &identity.ResourceRef{URI: uri.String()}, Document: json.RawMessage(`{"forged":true}`), SourceKind: "forged"}
	output := &CompileResult{}
	if err := method(context.Background(), input, output); err != nil {
		t.Fatal(err)
	}
	if compiled != 1 || output.Resource == nil || output.Resource.URI != uri.String() || output.Resource.ContentFingerprint != identity.ContentFingerprint(content) {
		t.Fatalf("missing output pin: %+v", output)
	}
	original := append(json.RawMessage(nil), content...)
	content = json.RawMessage(`{"schemaVersion":1,"reportDocument":{},"reportSpec":{"title":"changed-before-compile"}}`)
	if _, err := service.Compile(context.Background(), &CompileRequest{ResolvedResource: output.Resource}); err == nil || compiled != 1 {
		t.Fatal("working pin was re-resolved to newer content")
	}
	content = original
	allowed = false
	if err := method(context.Background(), input, &CompileResult{}); err == nil || compiled != 1 {
		t.Fatal("revoked resource reached compiler")
	}
	allowed = true
	changeDuringCompile = true
	if err := method(context.Background(), input, &CompileResult{}); err == nil {
		t.Fatal("compiler result released after source drift")
	}
	if _, err := service.Compile(context.Background(), &CompileRequest{ArtifactRef: "legacy-unmapped", Document: input.Document}); err == nil {
		t.Fatal("canonical mode allowed legacy unbound compilation")
	}
}

func TestPinnedAuthoredReportCompilesThroughOfficialLowerer(t *testing.T) {
	content, err := os.ReadFile("testdata/authored_report.json")
	if err != nil {
		t.Fatal(err)
	}
	uri, _ := identity.ParseResourceURI("report://steward/forecast_supply_command_center")
	source := &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) { return append(json.RawMessage(nil), content...), nil }}
	allowed := true
	resolver := &identity.ResourceResolver{Source: source, Policy: reportPolicyFunc(func(_ context.Context, _ identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
		if !allowed {
			return identity.ResourceDecision{}, identity.ErrResourceDenied
		}
		return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: "verified-account", ValidUntil: time.Now().Add(time.Minute)}, nil
	})}
	service := New(Options{Store: NewMemoryStore(), Compiler: NewReportSpecCompiler(time.Now), ResourceResolver: func(context.Context, string) (*identity.ResourceResolver, error) { return resolver, nil }})
	request := &CompileRequest{Resource: &identity.ResourceRef{URI: uri.String()}, Document: json.RawMessage(`{"forged":true}`)}
	result, err := service.Compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Resource == nil || result.Resource.URI != uri.String() || len(result.ReportSpec) == 0 {
		t.Fatalf("authored report failed actual compiler: %+v", result)
	}
	allowed = false
	if _, err := service.Compile(context.Background(), request); err == nil {
		t.Fatal("revoked authored resource compiled")
	}
	allowed = true
	var definition registry.ReportEnvelope
	if err = json.Unmarshal(content, &definition); err != nil {
		t.Fatal(err)
	}
	definition.BuilderRef = "other"
	content, _ = json.Marshal(definition)
	if _, err := service.Compile(context.Background(), request); err == nil {
		t.Fatal("invalid authored dependencies compiled")
	}
}

package reporting

import (
	"context"
	"errors"
	identity "github.com/viant/agently-core/protocol/resource"
	"testing"
)

func TestMissingResourceReaderDeniesBeforeGetOrCompiler(t *testing.T) {
	for _, scenario := range []string{"legacy-nil", "generic-nil", "typed-nil"} {
		t.Run(scenario, func(t *testing.T) {
			compileCalls := 0
			service := New(Options{Store: NewMemoryStore(), Compiler: reportCompilerFunc(func(context.Context, *CompileRequest) (*CompileResult, error) {
				compileCalls++
				return &CompileResult{}, nil
			})})
			switch scenario {
			case "legacy-nil":
				service.SetResourceResolver(func(context.Context, string) (*identity.ResourceResolver, error) { return nil, nil })
			case "generic-nil":
				service.SetResourceReaderFactory(func(context.Context, string) (ResourceReader, error) { return nil, nil })
			case "typed-nil":
				service.SetResourceReaderFactory(func(context.Context, string) (ResourceReader, error) {
					var reader *identity.ResourceResolver
					return reader, nil
				})
			}
			result, err := service.Compile(context.Background(), &CompileRequest{Resource: &identity.ResourceRef{URI: "report://example/missing"}})
			if result != nil || !errors.Is(err, identity.ErrResourceDenied) || compileCalls != 0 {
				t.Fatalf("missing reader reached compiler: %v %v calls=%d", result, err, compileCalls)
			}
			report, err := service.GetReport(context.Background(), &GetReportInput{ReportID: "report://example/missing"})
			if report != nil || !errors.Is(err, identity.ErrResourceDenied) {
				t.Fatalf("missing reader did not deny get: %v %v", report, err)
			}
		})
	}
}

func TestResourceReaderCancellationRetainsCauseBeforeFactory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	service := New(Options{Store: NewMemoryStore()})
	service.SetResourceReaderFactory(func(context.Context, string) (ResourceReader, error) { calls++; return nil, nil })
	_, err := service.ResourceReaderFor(ctx, "report.retrieve")
	if !errors.Is(err, context.Canceled) || errors.Is(err, identity.ErrResourceDenied) || calls != 0 {
		t.Fatalf("cancelled read: %v calls=%d", err, calls)
	}
}

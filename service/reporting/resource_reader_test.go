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
				service.resourceResolver = func(context.Context, string) (*identity.ResourceResolver, error) { return nil, nil }
			case "generic-nil":
				service.SetResourceReaderFactory(func(context.Context, string) (ResourceReader, error) { return nil, nil })
			case "typed-nil":
				service.SetResourceReaderFactory(func(context.Context, string) (ResourceReader, error) {
					var reader *identity.ResourceResolver
					return reader, nil
				})
			}
			result, err := service.Compile(context.Background(), &CompileRequest{Resource: &identity.ResourceRef{URI: "report://steward/missing"}})
			if result != nil || !errors.Is(err, identity.ErrResourceDenied) || compileCalls != 0 {
				t.Fatalf("missing reader reached compiler: %v %v calls=%d", result, err, compileCalls)
			}
			report, err := service.GetReport(context.Background(), &GetReportInput{ReportID: "report://steward/missing"})
			if report != nil || !errors.Is(err, identity.ErrResourceDenied) {
				t.Fatalf("missing reader did not deny get: %v %v", report, err)
			}
		})
	}
}

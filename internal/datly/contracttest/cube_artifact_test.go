package tests

import (
	"reflect"
	"testing"

	runcube "github.com/viant/agently-core/internal/datly/run/cube"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	"github.com/viant/datly/report"
	rhandler "github.com/viant/datly/runtime/handler"
	dtag "github.com/viant/datly/tag"
)

func linkedCubeArtifactInputs[CubeHolder, CubeInput, CubeOutput any](t *testing.T, resources *resource.Store, base *bootstrap.Artifact, input, output reflect.Type, factory func() (rhandler.TypedHandler, error)) []bootstrap.ArtifactInput {
	t.Helper()
	holder := reflect.TypeFor[CubeHolder]()
	field, ok := holder.FieldByName("Contract")
	if !ok {
		t.Fatal("linked cube holder contract absent")
	}
	tag, exists, err := dtag.ParseComponent(field.Tag)
	must(t, err)
	if !exists {
		t.Fatal("linked cube component tag absent")
	}
	source := &bootstrap.RouteSource{HolderType: holder.Name(), FieldName: field.Name, PackagePath: holder.PkgPath(), Tag: tag, InputType: reflect.TypeFor[CubeInput]().Name(), OutputType: reflect.TypeFor[CubeOutput]().Name()}
	cube, err := source.Resolve(reflect.TypeFor[CubeInput](), reflect.TypeFor[CubeOutput]())
	must(t, err)
	// Match the existing payloadArtifact test-only HTTP-binding exposure.
	// Production generated internal=true is unchanged.
	cube.Routes[0].Internal = false
	handler, err := factory()
	must(t, err)
	return []bootstrap.ArtifactInput{{Component: base.Component, InputType: input, OutputType: output, Resources: resources}, {Component: cube, InputType: reflect.TypeFor[CubeInput](), OutputType: reflect.TypeFor[CubeOutput](), Handler: handler, HandlerOwnedOutput: true, Resources: resources}}
}
func TestGeneratedCubeFacadesRegisterAlongsideOriginalReaders(t *testing.T) {
	resources := resource.New()
	must(t, resources.Register(runcube.ReaderDatlyResourceNamespace, runcube.ReaderDatlyResources))
	base := payloadArtifact(t, resources, reflect.TypeFor[runcube.ReaderComponent](), reflect.TypeFor[runcube.RunReportInput](), reflect.TypeFor[runcube.RunReportOutput]())
	compiler := report.NewProjectCompiler(report.ProjectConfig{})
	missing, err := compiler.CompileArtifacts([]bootstrap.ArtifactInput{{Component: base.Component, InputType: reflect.TypeFor[runcube.RunReportInput](), OutputType: reflect.TypeFor[runcube.RunReportOutput](), Resources: resources}})
	must(t, err)
	t.Logf("base-only artifacts=%d reportLinkedFacade=%t", len(missing.Artifacts()), base.Component.Settings.Report.LinkedFacade)
	complete, err := report.NewProjectCompiler(report.ProjectConfig{}).CompileArtifacts(linkedCubeArtifactInputs[runcube.ReaderCubeComponent, runcube.ReaderCubeInput, runcube.ReaderCubeOutput](t, resources, base, reflect.TypeFor[runcube.RunReportInput](), reflect.TypeFor[runcube.RunReportOutput](), runcube.NewReaderCube))
	must(t, err)
	if len(missing.Artifacts()) != 1 || len(complete.Artifacts()) != 2 {
		t.Fatalf("unexpected exact native artifact counts: missing=%d complete=%d", len(missing.Artifacts()), len(complete.Artifacts()))
	}
}

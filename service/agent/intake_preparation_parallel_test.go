package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/service/core"
	intakesvc "github.com/viant/agently-core/service/intake"
	forgeuisvc "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/agently-core/service/reporting/catalog"
)

type intakePreparationCatalog struct {
	catalog.Provider
	list func(context.Context) (*catalog.ReportCatalogResult, error)
}

func (p intakePreparationCatalog) List(ctx context.Context, _ catalog.ReportCatalogQuery) (*catalog.ReportCatalogResult, error) {
	return p.list(ctx)
}

type intakePreparationModel struct {
	request    *llm.GenerateRequest
	contextErr error
}

func (*intakePreparationModel) Implements(string) bool { return false }
func (m *intakePreparationModel) Generate(ctx context.Context, request *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.contextErr = ctx.Err()
	m.request = request
	if m.contextErr != nil {
		return nil, m.contextErr
	}
	return &llm.GenerateResponse{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: `{"classification":{"intent":"analysis","confidence":0.99}}`}, FinishReason: "stop"}}}, nil
}
func intakePreparationInput() *QueryInput {
	return &QueryInput{Query: "Explain available records", Agent: &agentmdl.Agent{Intake: agentmdl.Intake{Enabled: true, Model: "synthetic-model", Scope: []string{agentmdl.IntakeScopeTools}}}}
}

func TestIntakePreparationOverlapsMetadataAndPreservesParentContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	entered := make(chan string, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	reg := &parallelIntakeRegistry{fakeRegistry: fakeRegistry{defs: []llm.ToolDefinition{{Name: "inventory:rank", Description: "Rank inventory"}}}, entered: entered, release: release}
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{ReportCatalog: intakePreparationCatalog{list: func(ctx context.Context) (*catalog.ReportCatalogResult, error) {
		entered <- "ui"
		select {
		case <-release:
			return &catalog.ReportCatalogResult{Reports: []catalog.ReportCatalogEntry{{ReportCatalogCandidate: catalog.ReportCatalogCandidate{URI: "report://example/summary", Title: "Example summary"}}}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}})
	model := &intakePreparationModel{}
	llmSvc := core.New(&plannerRoutingFinder{model: model}, nil, nil)
	svc := &Service{registry: reg, intakeSvc: intakesvc.New(llmSvc), uiBridge: bridge}
	input := intakePreparationInput()
	done := make(chan struct{})
	go func() { defer close(done); svc.maybeRunIntakeSidecar(ctx, input) }()
	seen := []string{}
	for range 2 {
		select {
		case stage := <-entered:
			seen = append(seen, stage)
		case <-ctx.Done():
			t.Fatal("UI and capability metadata did not start before either completed")
		}
	}
	require.ElementsMatch(t, []string{"ui", "tools"}, seen)
	releaseOnce.Do(func() { close(release) })
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, model.contextErr)
	require.NotNil(t, model.request, "joined metadata must reach the actual intake model")
	var text strings.Builder
	for _, message := range model.request.Messages {
		text.WriteString(message.Content)
	}
	prompt := text.String()
	require.Contains(t, prompt, "Example summary")
	require.Contains(t, prompt, "Rank inventory")
	require.Less(t, strings.Index(prompt, "Workspace UI bootstrap:"), strings.Index(prompt, "Client context (transport facts"))
	require.Less(t, strings.Index(prompt, "Client context (transport facts"), strings.Index(prompt, "Available tools and skills"))
	require.NotNil(t, intakesvc.FromContext(input.Context))
}

func TestIntakePreparationSkipsMetadataBeforeResolvedRouting(t *testing.T) {
	for _, mode := range []string{"disabled", "no service", "explicit profile", "empty query", "caller override", "activation override"} {
		t.Run(mode, func(t *testing.T) {
			var reads atomic.Int32
			bridge := forgeuisvc.NewService(&forgeuisvc.Config{ReportCatalog: intakePreparationCatalog{list: func(context.Context) (*catalog.ReportCatalogResult, error) {
				reads.Add(1)
				return nil, errors.New("unexpected metadata read")
			}}})
			svc := &Service{intakeSvc: intakesvc.New(nil), uiBridge: bridge}
			input := intakePreparationInput()
			switch mode {
			case "disabled":
				input.Agent.Intake.Enabled = false
			case "no service":
				svc.intakeSvc = nil
			case "explicit profile":
				input.PromptProfileId = "selected"
			case "empty query":
				input.Query = " "
			case "caller override":
				input.Context, _ = intakesvc.StoreCallerProvided(nil, &intakesvc.Context{Classification: intakesvc.ClassificationContext{Intent: "caller"}})
			case "activation override":
				input.Agent.Intake.ActivationRules = []agentmdl.ActivationRule{{ID: "example", Match: agentmdl.ActivationMatch{Patterns: []string{"^Explain available records$"}}, Classification: agentmdl.ActivationClassification{Intent: "declared"}}}
			}
			svc.maybeRunIntakeSidecar(context.Background(), input)
			require.Zero(t, reads.Load())
			if mode == "caller override" || mode == "activation override" {
				require.NotNil(t, intakesvc.FromContext(input.Context))
			}
		})
	}
}

func TestIntakePreparationCapabilityFailureRemainsAdvisory(t *testing.T) {
	svc, registry := selectedSchemaService()
	model := &intakePreparationModel{}
	svc.intakeSvc = intakesvc.New(core.New(&plannerRoutingFinder{model: model}, nil, nil))
	svc.uiBridge = forgeuisvc.NewService(&forgeuisvc.Config{ReportCatalog: intakePreparationCatalog{list: func(context.Context) (*catalog.ReportCatalogResult, error) {
		return &catalog.ReportCatalogResult{Reports: []catalog.ReportCatalogEntry{{ReportCatalogCandidate: catalog.ReportCatalogCandidate{URI: "report://example/summary", Title: "Example summary"}}}}, nil
	}}})
	input := intakePreparationInput()
	input.ToolBundles = []string{"required"}
	svc.maybeRunIntakeSidecar(context.Background(), input)
	require.NotEmpty(t, registry.patterns)
	require.NotNil(t, model.request, "unavailable capability metadata must not suppress intake")
	require.NoError(t, model.contextErr)
	var text strings.Builder
	for _, message := range model.request.Messages {
		text.WriteString(message.Content)
	}
	require.Contains(t, text.String(), "Example summary")
	require.NotContains(t, text.String(), "Available tools and skills (capability metadata")
	require.NotNil(t, intakesvc.FromContext(input.Context))
}

type intakePreparationScope struct{ finalized *atomic.Int32 }

func (s intakePreparationScope) BeginMetadataRead(ctx context.Context) (context.Context, func() error, error) {
	return ctx, func() error { s.finalized.Add(1); return errors.New("synthetic authority revision changed") }, nil
}
func (intakePreparationScope) WithoutMetadataRead(ctx context.Context) context.Context { return ctx }

func TestIntakePreparationDiscardsMetadataWhenScopeFinalizationFails(t *testing.T) {
	var finalized atomic.Int32
	model := &intakePreparationModel{}
	bridge := forgeuisvc.NewService(&forgeuisvc.Config{MetadataScope: intakePreparationScope{finalized: &finalized}, ReportCatalog: intakePreparationCatalog{list: func(context.Context) (*catalog.ReportCatalogResult, error) {
		return &catalog.ReportCatalogResult{Reports: []catalog.ReportCatalogEntry{{ReportCatalogCandidate: catalog.ReportCatalogCandidate{URI: "report://example/summary", Title: "Unverified summary"}}}}, nil
	}}})
	svc := &Service{intakeSvc: intakesvc.New(core.New(&plannerRoutingFinder{model: model}, nil, nil)), uiBridge: bridge}
	svc.maybeRunIntakeSidecar(context.Background(), intakePreparationInput())
	require.Equal(t, int32(1), finalized.Load())
	require.NotNil(t, model.request)
	require.NoError(t, model.contextErr)
	for _, message := range model.request.Messages {
		require.NotContains(t, message.Content, "Unverified summary")
	}
}

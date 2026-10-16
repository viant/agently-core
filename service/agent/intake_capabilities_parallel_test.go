package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	skillproto "github.com/viant/agently-core/protocol/skill"
)

type parallelIntakeRegistry struct {
	fakeRegistry
	once    sync.Once
	entered chan string
	release chan struct{}
}

func (r *parallelIntakeRegistry) wait() { r.once.Do(func() { r.entered <- "tools"; <-r.release }) }
func (r *parallelIntakeRegistry) Definitions() []llm.ToolDefinition {
	r.wait()
	return r.fakeRegistry.Definitions()
}
func (r *parallelIntakeRegistry) MatchDefinition(pattern string) []*llm.ToolDefinition {
	r.wait()
	return r.fakeRegistry.MatchDefinition(pattern)
}
func (r *parallelIntakeRegistry) GetDefinition(name string) (*llm.ToolDefinition, bool) {
	r.wait()
	return r.fakeRegistry.GetDefinition(name)
}

type parallelIntakeSkills struct {
	entered chan string
	release chan struct{}
}

func (s parallelIntakeSkills) ReadLocal(_ context.Context, item *skillproto.Skill) (*skillproto.Skill, error) {
	return item, nil
}
func (s parallelIntakeSkills) Read(context.Context, string) (*skillproto.Skill, bool, error) {
	return nil, false, nil
}
func (s parallelIntakeSkills) List(ctx context.Context) ([]skillproto.Metadata, error) {
	s.entered <- "skills"
	select {
	case <-s.release:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestIntakeToolAndSkillMetadataAreConcurrent(t *testing.T) {
	entered := make(chan string, 2)
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var once sync.Once
	defer once.Do(func() { close(release) })
	reg := &parallelIntakeRegistry{fakeRegistry: fakeRegistry{defs: []llm.ToolDefinition{{Name: "inventory:rank", Description: "Rank inventory"}}}, entered: entered, release: release}
	skills := newLoadedTestSkillService(t, t.TempDir(), "catalog-help")
	skills.SetDelivery(nil, parallelIntakeSkills{entered: entered, release: release})
	s := &Service{registry: reg, skillSvc: skills}
	input := &QueryInput{Agent: &agentmdl.Agent{Skills: []string{"catalog-help"}, Tool: agentmdl.Tool{Items: []*llm.Tool{{Name: "inventory:rank"}}}}}
	type output struct {
		summary string
		err     error
	}
	done := make(chan output, 1)
	go func() { value, err := s.intakeCapabilitySummary(ctx, input); done <- output{value, err} }()
	seen := []string{}
	for range 2 {
		select {
		case name := <-entered:
			seen = append(seen, name)
		case <-ctx.Done():
			t.Fatal("tool and skill reads did not both start before either completed")
		}
	}
	require.ElementsMatch(t, []string{"tools", "skills"}, seen)
	once.Do(func() { close(release) })
	select {
	case result := <-done:
		require.NoError(t, result.err)
		require.Contains(t, result.summary, "Rank inventory")
	case <-ctx.Done():
		t.Fatal("metadata merge did not complete")
	}
}

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	execconfig "github.com/viant/agently-core/app/executor/config"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/binding"
	skillproto "github.com/viant/agently-core/protocol/skill"
	agruntime "github.com/viant/agently-core/runtime"
	"github.com/viant/agently-core/runtime/toolselection"
	"github.com/viant/agently-core/service/intake"
	skillsvc "github.com/viant/agently-core/service/skill"
)

// This isolates binding work and schema volume, not network or model latency.
// The same benchmark can run against the baseline using a Go overlay.
func BenchmarkSelectedSurfaceWithInlineSkill(b *testing.B) {
	for _, size := range []int{100, 300} {
		b.Run(fmt.Sprintf("catalog_%d", size), func(b *testing.B) {
			s := selectionService()
			properties := map[string]interface{}{}
			for i := 0; i < 200; i++ {
				properties[fmt.Sprintf("field_%03d", i)] = map[string]interface{}{"type": "string", "description": "An optional field in the data query."}
			}
			defs := make([]llm.ToolDefinition, 0, size)
			for i := 0; i < size; i++ {
				defs = append(defs, llm.ToolDefinition{Name: fmt.Sprintf("catalog:method%03d", i), Description: "A catalog data operation.", Parameters: map[string]interface{}{"type": "object", "properties": properties}})
			}
			s.registry.(*fakeRegistry).defs = defs
			root := b.TempDir()
			dir := filepath.Join(root, "format-result")
			if err := os.MkdirAll(dir, 0755); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: format-result\ndescription: Format a result.\nallowed-tools: catalog:method001\nmetadata:\n  agently-context: inline\n---\nFormat the result.\n"), 0644); err != nil {
				b.Fatal(err)
			}
			s.defaults = &execconfig.Defaults{Skills: execconfig.SkillsDefaults{Roots: []string{root}}}
			s.skillSvc = skillsvc.New(s.defaults, nil, nil)
			if err := s.skillSvc.Load(context.Background()); err != nil {
				b.Fatal(err)
			}
			in := selectionInput(&intake.ToolSelection{Names: []string{"catalog:method000"}, Complete: true})
			in.Agent.Skills = []string{"format-result"}
			in.Runtime = &agruntime.Context{SkillActivation: &skillproto.ActivationContext{Name: "format-result", Body: "Format the result.", Mode: "inline"}}
			ctx := toolselection.WithState(context.Background(), nil)
			var result *binding.Binding
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				selected, err := s.buildToolSignatures(ctx, in)
				if err != nil {
					b.Fatal(err)
				}
				result = &binding.Binding{}
				result.Tools.Signatures = selected
				if err = s.applyActiveSkillToolSurface(ctx, in, result); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			encoded, err := json.Marshal(result.Tools.Signatures)
			if err != nil {
				b.Fatal(err)
			}
			present := 0.0
			for _, name := range signatureNames(result.Tools.Signatures) {
				if name == "catalog-method000" {
					present = 1
				}
			}
			b.ReportMetric(float64(len(result.Tools.Signatures)), "schemas")
			b.ReportMetric(float64(len(encoded)), "schema_bytes")
			b.ReportMetric(present, "selected_present")
		})
	}
}

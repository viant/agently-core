package agent

import (
	"testing"

	"github.com/viant/agently-core/protocol/binding"
)

func TestRuntimeActivatedSkill(t *testing.T) {
	input := &QueryInput{
		Context: map[string]interface{}{
			"skillActivationName": "forecasting-cube",
			"skillActivationBody": "Loaded skill body",
		},
	}
	name, body, ok := runtimeActivatedSkill(input)
	if !ok {
		t.Fatalf("expected active skill context")
	}
	if name != "forecasting-cube" {
		t.Fatalf("name = %q", name)
	}
	if body != "Loaded skill body" {
		t.Fatalf("body = %q", body)
	}
}

func TestResolveActiveSkillNames_FallsBackToContext(t *testing.T) {
	input := &QueryInput{
		Context: map[string]interface{}{
			"skillActivationName": "forecasting-cube",
			"skillActivationBody": "Loaded skill body",
		},
	}
	names := resolveActiveSkillNames(&binding.History{}, input, nil, nil, "", "")
	if len(names) != 1 || names[0] != "forecasting-cube" {
		t.Fatalf("names = %#v", names)
	}
}

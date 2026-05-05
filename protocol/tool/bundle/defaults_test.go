package bundle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
)

func TestDeriveBundles_SystemPatchExcludesCommitRollback(t *testing.T) {
	defs := []llm.ToolDefinition{
		{Name: "system/patch:apply"},
		{Name: "system/patch:replace"},
		{Name: "system/patch:snapshot"},
		{Name: "system/patch:commit"},
		{Name: "system/patch:rollback"},
	}

	bundles := DeriveBundles(defs)

	var patchBundle *Bundle
	for _, item := range bundles {
		if item.ID == "system/patch" {
			patchBundle = item
			break
		}
	}
	if assert.NotNil(t, patchBundle) {
		assert.EqualValues(t, []llm.Tool{
			{Name: "system/patch:apply"},
			{Name: "system/patch:replace"},
			{Name: "system/patch:snapshot"},
		}, patchBundle.Match)
	}
}

func TestDeriveBundles_ScratchpadUsesDefaultIcon(t *testing.T) {
	bundles := DeriveBundles([]llm.ToolDefinition{
		{Name: "scratchpad:memorize"},
		{Name: "scratchpad:append"},
		{Name: "scratchpad:list"},
		{Name: "scratchpad:fetch"},
	})

	var scratchpadBundle *Bundle
	for _, item := range bundles {
		if item.ID == "scratchpad" {
			scratchpadBundle = item
			break
		}
	}
	if assert.NotNil(t, scratchpadBundle) {
		assert.Equal(t, "builtin:scratchpad", scratchpadBundle.IconRef)
		assert.EqualValues(t, []llm.Tool{{Name: "scratchpad/*"}}, scratchpadBundle.Match)
	}
}

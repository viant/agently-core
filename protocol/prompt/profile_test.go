package prompt

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProfile_EffectiveMessages_Messages(t *testing.T) {
	p := &Profile{
		ID: "test",
		Messages: []Message{
			{Role: "system", Text: "You are a helpful assistant."},
			{Role: "user", Text: "Hello!"},
		},
	}
	msgs := p.EffectiveMessages()
	assert.Len(t, msgs, 2)
	assert.Equal(t, "system", msgs[0].Role)
	assert.Equal(t, "You are a helpful assistant.", msgs[0].Text)
}

func TestProfile_EffectiveMessages_Instructions(t *testing.T) {
	p := &Profile{
		ID:           "test",
		Instructions: "You are a performance analyst.",
	}
	msgs := p.EffectiveMessages()
	assert.Len(t, msgs, 1)
	assert.Equal(t, "system", msgs[0].Role)
	assert.Equal(t, "You are a performance analyst.", msgs[0].Text)
}

func TestProfile_EffectiveMessages_MCPOnly(t *testing.T) {
	p := &Profile{
		ID:  "test",
		MCP: &MCPSource{Server: "myserver", Prompt: "myprompt"},
	}
	msgs := p.EffectiveMessages()
	assert.Nil(t, msgs)
}

func TestProfile_EffectiveMessages_MessagesTakePriority(t *testing.T) {
	p := &Profile{
		ID:           "test",
		Instructions: "fallback",
		Messages: []Message{
			{Role: "system", Text: "priority message"},
		},
	}
	msgs := p.EffectiveMessages()
	assert.Len(t, msgs, 1)
	assert.Equal(t, "priority message", msgs[0].Text)
}

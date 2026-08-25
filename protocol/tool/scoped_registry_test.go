package tool

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

type captureRegistry struct {
	lastConversationID string
	retryPolicies      map[string]bool
	protectedTools     map[string]bool
}

func (c *captureRegistry) Definitions() []llm.ToolDefinition { return nil }
func (c *captureRegistry) MatchDefinition(string) []*llm.ToolDefinition {
	return nil
}
func (c *captureRegistry) GetDefinition(string) (*llm.ToolDefinition, bool) { return nil, false }
func (c *captureRegistry) MustHaveTools([]string) ([]llm.Tool, error)       { return nil, nil }
func (c *captureRegistry) Execute(ctx context.Context, _ string, _ map[string]interface{}) (string, error) {
	c.lastConversationID = memory.ConversationIDFromContext(ctx)
	return "", nil
}
func (c *captureRegistry) SetDebugLogger(io.Writer) {}
func (c *captureRegistry) Initialize(context.Context) {
}
func (c *captureRegistry) ToolRetryable(name string) (bool, bool) {
	retryable, ok := c.retryPolicies[name]
	return retryable, ok
}
func (c *captureRegistry) ToolExecutionProtected(name string) bool {
	return c.protectedTools[name]
}

func TestScopedRegistry_InjectsConversationID_WhenModelMessageIDPresent(t *testing.T) {
	inner := &captureRegistry{}
	reg := WithConversation(inner, "conv-123")

	ctx := context.WithValue(context.Background(), memory.ModelMessageIDKey, "msg-1")
	_, err := reg.Execute(ctx, "noop", nil)
	assert.NoError(t, err)
	assert.Equal(t, "conv-123", inner.lastConversationID)
}

func TestScopedRegistry_DelegatesRetryPolicy(t *testing.T) {
	inner := &captureRegistry{retryPolicies: map[string]bool{"ui/report:run": false}}
	reg := WithConversation(inner, "conv-123")

	resolver, ok := reg.(RetryResolver)
	assert.True(t, ok)
	retryable, configured := resolver.ToolRetryable("ui/report:run")
	assert.True(t, configured)
	assert.False(t, retryable)
}

func TestScopedRegistry_DelegatesExecutionProtection(t *testing.T) {
	inner := &captureRegistry{protectedTools: map[string]bool{"sendgrid/sendgridSendMail": true}}
	reg := WithConversation(inner, "conv-123")

	resolver, ok := reg.(ExecutionProtectionResolver)
	assert.True(t, ok)
	assert.True(t, resolver.ToolExecutionProtected("sendgrid/sendgridSendMail"))
	assert.False(t, resolver.ToolExecutionProtected("ui/report:run"))
}

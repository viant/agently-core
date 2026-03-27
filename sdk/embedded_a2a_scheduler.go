package sdk

import (
	"context"
	"errors"

	"github.com/viant/agently-core/service/a2a"
	"github.com/viant/agently-core/service/scheduler"
)

func (c *EmbeddedClient) TerminateConversation(ctx context.Context, conversationID string) error {
	if c.agent == nil {
		return errors.New("agent service not configured")
	}
	return c.agent.Terminate(ctx, conversationID)
}

func (c *EmbeddedClient) CompactConversation(ctx context.Context, conversationID string) error {
	if c.agent == nil {
		return errors.New("agent service not configured")
	}
	return c.agent.Compact(ctx, conversationID)
}

func (c *EmbeddedClient) PruneConversation(ctx context.Context, conversationID string) error {
	if c.agent == nil {
		return errors.New("agent service not configured")
	}
	return c.agent.Prune(ctx, conversationID)
}

func (c *EmbeddedClient) GetA2AAgentCard(ctx context.Context, agentID string) (*a2a.AgentCard, error) {
	if c.a2aSvc == nil {
		return nil, errors.New("A2A service not configured")
	}
	return c.a2aSvc.GetAgentCard(ctx, agentID)
}

func (c *EmbeddedClient) SendA2AMessage(ctx context.Context, agentID string, req *a2a.SendMessageRequest) (*a2a.SendMessageResponse, error) {
	if c.a2aSvc == nil {
		return nil, errors.New("A2A service not configured")
	}
	return c.a2aSvc.SendMessage(ctx, agentID, req)
}

func (c *EmbeddedClient) ListA2AAgents(ctx context.Context, agentIDs []string) ([]string, error) {
	if c.a2aSvc == nil {
		return nil, errors.New("A2A service not configured")
	}
	return c.a2aSvc.ListA2AAgents(ctx, agentIDs)
}

func (c *EmbeddedClient) SetScheduler(svc *scheduler.Service) {
	c.schedulerSvc = svc
}

func (c *EmbeddedClient) GetSchedule(ctx context.Context, id string) (*scheduler.Schedule, error) {
	if c.schedulerSvc == nil {
		return nil, errors.New("scheduler service not configured")
	}
	return c.schedulerSvc.Get(ctx, id)
}

func (c *EmbeddedClient) ListSchedules(ctx context.Context) ([]*scheduler.Schedule, error) {
	if c.schedulerSvc == nil {
		return nil, errors.New("scheduler service not configured")
	}
	return c.schedulerSvc.List(ctx)
}

func (c *EmbeddedClient) UpsertSchedules(ctx context.Context, schedules []*scheduler.Schedule) error {
	if c.schedulerSvc == nil {
		return errors.New("scheduler service not configured")
	}
	for _, s := range schedules {
		if err := c.schedulerSvc.Upsert(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

func (c *EmbeddedClient) RunScheduleNow(ctx context.Context, id string) error {
	if c.schedulerSvc == nil {
		return errors.New("scheduler service not configured")
	}
	return c.schedulerSvc.RunNow(ctx, id)
}

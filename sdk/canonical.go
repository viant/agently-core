package sdk

import api "github.com/viant/agently-core/sdk/api"

type ActiveFeedState = api.ActiveFeedState
type ConversationState = api.ConversationState
type TurnState = api.TurnState
type TurnStatus = api.TurnStatus
type UserMessageState = api.UserMessageState
type TurnMessageState = api.TurnMessageState
type AssistantState = api.AssistantState
type AssistantMessageState = api.AssistantMessageState
type ExecutionState = api.ExecutionState
type ExecutionPageState = api.ExecutionPageState
type ModelStepState = api.ModelStepState
type ToolStepState = api.ToolStepState
type AsyncOperationState = api.AsyncOperationState
type ElicitationState = api.ElicitationState
type ElicitationStatus = api.ElicitationStatus
type LinkedConversationState = api.LinkedConversationState
type PlanFeedPayload = api.PlanFeedPayload
type PlanStep = api.PlanStep
type UsageSummary = api.UsageSummary
type ConversationStateResponse = api.ConversationStateResponse

const (
	TurnStatusQueued         = api.TurnStatusQueued
	TurnStatusRunning        = api.TurnStatusRunning
	TurnStatusWaitingForUser = api.TurnStatusWaitingForUser
	TurnStatusCompleted      = api.TurnStatusCompleted
	TurnStatusFailed         = api.TurnStatusFailed
	TurnStatusCanceled       = api.TurnStatusCanceled
)

const (
	ElicitationStatusPending  = api.ElicitationStatusPending
	ElicitationStatusAccepted = api.ElicitationStatusAccepted
	ElicitationStatusDeclined = api.ElicitationStatusDeclined
	ElicitationStatusCanceled = api.ElicitationStatusCanceled
)

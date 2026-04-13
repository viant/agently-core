package sdk

import "github.com/viant/agently-core/sdkapi"

type ActiveFeedState = sdkapi.ActiveFeedState
type ConversationState = sdkapi.ConversationState
type TurnState = sdkapi.TurnState
type TurnStatus = sdkapi.TurnStatus
type UserMessageState = sdkapi.UserMessageState
type AssistantState = sdkapi.AssistantState
type AssistantMessageState = sdkapi.AssistantMessageState
type ExecutionState = sdkapi.ExecutionState
type ExecutionPageState = sdkapi.ExecutionPageState
type ModelStepState = sdkapi.ModelStepState
type ToolStepState = sdkapi.ToolStepState
type AsyncOperationState = sdkapi.AsyncOperationState
type ElicitationState = sdkapi.ElicitationState
type ElicitationStatus = sdkapi.ElicitationStatus
type LinkedConversationState = sdkapi.LinkedConversationState
type PlanFeedPayload = sdkapi.PlanFeedPayload
type PlanStep = sdkapi.PlanStep
type UsageSummary = sdkapi.UsageSummary
type ConversationStateResponse = sdkapi.ConversationStateResponse

const (
	TurnStatusQueued         = sdkapi.TurnStatusQueued
	TurnStatusRunning        = sdkapi.TurnStatusRunning
	TurnStatusWaitingForUser = sdkapi.TurnStatusWaitingForUser
	TurnStatusCompleted      = sdkapi.TurnStatusCompleted
	TurnStatusFailed         = sdkapi.TurnStatusFailed
	TurnStatusCanceled       = sdkapi.TurnStatusCanceled
)

const (
	ElicitationStatusPending  = sdkapi.ElicitationStatusPending
	ElicitationStatusAccepted = sdkapi.ElicitationStatusAccepted
	ElicitationStatusDeclined = sdkapi.ElicitationStatusDeclined
	ElicitationStatusCanceled = sdkapi.ElicitationStatusCanceled
)

package sdk

import "github.com/viant/agently-core/sdkapi"

type PageInput = sdkapi.PageInput
type Direction = sdkapi.Direction

const (
	DirectionBefore = sdkapi.DirectionBefore
	DirectionAfter  = sdkapi.DirectionAfter
	DirectionLatest = sdkapi.DirectionLatest
)

type MessagePage = sdkapi.MessagePage
type ConversationPage = sdkapi.ConversationPage
type GetMessagesInput = sdkapi.GetMessagesInput
type ListConversationsInput = sdkapi.ListConversationsInput
type ListLinkedConversationsInput = sdkapi.ListLinkedConversationsInput
type LinkedConversationEntry = sdkapi.LinkedConversationEntry
type LinkedConversationPage = sdkapi.LinkedConversationPage
type SteerTurnInput = sdkapi.SteerTurnInput
type SteerTurnOutput = sdkapi.SteerTurnOutput
type MoveQueuedTurnInput = sdkapi.MoveQueuedTurnInput
type EditQueuedTurnInput = sdkapi.EditQueuedTurnInput
type CreateConversationInput = sdkapi.CreateConversationInput
type UpdateConversationInput = sdkapi.UpdateConversationInput
type StreamEventsInput = sdkapi.StreamEventsInput
type ResolveElicitationInput = sdkapi.ResolveElicitationInput
type ListPendingElicitationsInput = sdkapi.ListPendingElicitationsInput
type PendingElicitation = sdkapi.PendingElicitation
type WorkspaceDefaults = sdkapi.WorkspaceDefaults
type WorkspaceCapabilities = sdkapi.WorkspaceCapabilities
type StarterTask = sdkapi.StarterTask
type WorkspaceAgentInfo = sdkapi.WorkspaceAgentInfo
type WorkspaceModelInfo = sdkapi.WorkspaceModelInfo
type WorkspaceMetadata = sdkapi.WorkspaceMetadata
type ListPendingToolApprovalsInput = sdkapi.ListPendingToolApprovalsInput
type PendingToolApproval = sdkapi.PendingToolApproval
type ApprovalOption = sdkapi.ApprovalOption
type ApprovalEditor = sdkapi.ApprovalEditor
type ApprovalCallback = sdkapi.ApprovalCallback
type ApprovalForgeView = sdkapi.ApprovalForgeView
type ApprovalMeta = sdkapi.ApprovalMeta
type ApprovalCallbackPayload = sdkapi.ApprovalCallbackPayload
type ApprovalCallbackResult = sdkapi.ApprovalCallbackResult
type PendingToolApprovalPage = sdkapi.PendingToolApprovalPage
type DecideToolApprovalInput = sdkapi.DecideToolApprovalInput
type DecideToolApprovalOutput = sdkapi.DecideToolApprovalOutput
type UploadFileInput = sdkapi.UploadFileInput
type UploadFileOutput = sdkapi.UploadFileOutput
type DownloadFileInput = sdkapi.DownloadFileInput
type DownloadFileOutput = sdkapi.DownloadFileOutput
type ListFilesInput = sdkapi.ListFilesInput
type FileEntry = sdkapi.FileEntry
type ListFilesOutput = sdkapi.ListFilesOutput
type ToolDefinitionInfo = sdkapi.ToolDefinitionInfo
type ResourceRef = sdkapi.ResourceRef
type ListResourcesInput = sdkapi.ListResourcesInput
type ListResourcesOutput = sdkapi.ListResourcesOutput
type GetResourceOutput = sdkapi.GetResourceOutput
type SaveResourceInput = sdkapi.SaveResourceInput
type Resource = sdkapi.Resource
type ExportResourcesInput = sdkapi.ExportResourcesInput
type ExportResourcesOutput = sdkapi.ExportResourcesOutput
type ImportResourcesInput = sdkapi.ImportResourcesInput
type ImportResourcesOutput = sdkapi.ImportResourcesOutput
type GetTranscriptInput = sdkapi.GetTranscriptInput
type QuerySelector = sdkapi.QuerySelector

type TranscriptOption func(*transcriptOptions)

type transcriptOptions struct {
	selectors    map[string]*QuerySelector
	includeFeeds bool
}

const (
	TranscriptSelectorTurn        = "Transcript"
	TranscriptSelectorMessage     = "Message"
	TranscriptSelectorToolMessage = "ToolMessage"
)

func ensureTranscriptSelector(o *transcriptOptions, name string) *QuerySelector {
	if o == nil || name == "" {
		return nil
	}
	if o.selectors == nil {
		o.selectors = map[string]*QuerySelector{}
	}
	if o.selectors[name] == nil {
		o.selectors[name] = &QuerySelector{}
	}
	return o.selectors[name]
}

func WithTranscriptSelector(name string, selector *QuerySelector) TranscriptOption {
	return func(o *transcriptOptions) {
		if selector == nil {
			return
		}
		if o.selectors == nil {
			o.selectors = map[string]*QuerySelector{}
		}
		o.selectors[name] = selector
	}
}

func WithIncludeFeeds() TranscriptOption {
	return func(o *transcriptOptions) {
		o.includeFeeds = true
	}
}

func WithTranscriptTurnSelector(selector *QuerySelector) TranscriptOption {
	return WithTranscriptSelector(TranscriptSelectorTurn, selector)
}

func WithTranscriptMessageSelector(selector *QuerySelector) TranscriptOption {
	return WithTranscriptSelector(TranscriptSelectorMessage, selector)
}

func WithTranscriptToolMessageSelector(selector *QuerySelector) TranscriptOption {
	return WithTranscriptSelector(TranscriptSelectorToolMessage, selector)
}

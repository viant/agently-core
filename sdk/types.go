package sdk

import api "github.com/viant/agently-core/sdk/api"

type PageInput = api.PageInput
type Direction = api.Direction

const (
	DirectionBefore = api.DirectionBefore
	DirectionAfter  = api.DirectionAfter
	DirectionLatest = api.DirectionLatest
)

type MessagePage = api.MessagePage
type ConversationPage = api.ConversationPage
type GetMessagesInput = api.GetMessagesInput
type ListConversationsInput = api.ListConversationsInput
type ListLinkedConversationsInput = api.ListLinkedConversationsInput
type LinkedConversationEntry = api.LinkedConversationEntry
type LinkedConversationPage = api.LinkedConversationPage
type SteerTurnInput = api.SteerTurnInput
type SteerTurnOutput = api.SteerTurnOutput
type MoveQueuedTurnInput = api.MoveQueuedTurnInput
type EditQueuedTurnInput = api.EditQueuedTurnInput
type CreateConversationInput = api.CreateConversationInput
type UpdateConversationInput = api.UpdateConversationInput
type StreamEventsInput = api.StreamEventsInput
type ResolveElicitationInput = api.ResolveElicitationInput
type ListPendingElicitationsInput = api.ListPendingElicitationsInput
type PendingElicitation = api.PendingElicitation
type WorkspaceDefaults = api.WorkspaceDefaults
type WorkspaceCapabilities = api.WorkspaceCapabilities
type MetadataTargetContext struct {
	Platform     string
	FormFactor   string
	Surface      string
	Capabilities []string
}
type StarterTask = api.StarterTask
type WorkspaceAgentInfo = api.WorkspaceAgentInfo
type WorkspaceModelInfo = api.WorkspaceModelInfo
type WorkspaceMetadata = api.WorkspaceMetadata
type ListPendingToolApprovalsInput = api.ListPendingToolApprovalsInput
type PendingToolApproval = api.PendingToolApproval
type ApprovalOption = api.ApprovalOption
type ApprovalEditor = api.ApprovalEditor
type ApprovalCallback = api.ApprovalCallback
type ApprovalForgeView = api.ApprovalForgeView
type ApprovalMeta = api.ApprovalMeta
type ApprovalCallbackPayload = api.ApprovalCallbackPayload
type ApprovalCallbackResult = api.ApprovalCallbackResult
type PendingToolApprovalPage = api.PendingToolApprovalPage
type DecideToolApprovalInput = api.DecideToolApprovalInput
type DecideToolApprovalOutput = api.DecideToolApprovalOutput
type UploadFileInput = api.UploadFileInput
type UploadFileOutput = api.UploadFileOutput
type DownloadFileInput = api.DownloadFileInput
type DownloadFileOutput = api.DownloadFileOutput
type ListFilesInput = api.ListFilesInput
type FileEntry = api.FileEntry
type ListFilesOutput = api.ListFilesOutput
type ToolDefinitionInfo = api.ToolDefinitionInfo
type ResourceRef = api.ResourceRef
type ListResourcesInput = api.ListResourcesInput
type ListResourcesOutput = api.ListResourcesOutput
type GetResourceOutput = api.GetResourceOutput
type SaveResourceInput = api.SaveResourceInput
type Resource = api.Resource
type ExportResourcesInput = api.ExportResourcesInput
type ExportResourcesOutput = api.ExportResourcesOutput
type ImportResourcesInput = api.ImportResourcesInput
type ImportResourcesOutput = api.ImportResourcesOutput
type GetTranscriptInput = api.GetTranscriptInput
type QuerySelector = api.QuerySelector

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

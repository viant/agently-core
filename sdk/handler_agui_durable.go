package sdk

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/viant/agently-core/service/browsermcp"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	aguistore "github.com/viant/agently-core/app/store/agui"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/protocol/agui/extensions"
	"github.com/viant/agently-core/runtime/aguistate"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/runtime/mcpapps"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
	svcauth "github.com/viant/agently-core/service/auth"
)

type aguiPending struct {
	ClientTools  []clienttool.PendingCall `json:"clientTools,omitempty"`
	Interrupts   []agui.WireInterrupt     `json:"interrupts,omitempty"`
	Dependencies []clienttool.Dependency  `json:"dependencies,omitempty"`
}

// The durable handler observes the journal, not the runtime subscription. The
// worker continues recording after an HTTP disconnect, and a retry reattaches
// without executing an admitted input again.
func handleAGUIDurable(client Client, runtime aguiRuntime, authCfg *svcauth.Config, bindings ...AGUIWorkspaceBindings) http.HandlerFunc {
	store := runtime.aguiStore()
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
		if err != nil {
			httpError(w, 400, err)
			return
		}
		if err = agui.ValidateInput(body); err != nil {
			httpError(w, 400, err)
			return
		}
		var input agui.RunAgentInput
		if err = json.Unmarshal(body, &input); err != nil {
			httpError(w, 400, err)
			return
		}
		if input.ProtocolVersion != "" && input.ProtocolVersion != agui.ProtocolVersion {
			httpError(w, 400, fmt.Errorf("unsupported protocolVersion %q; supported version is %s", input.ProtocolVersion, agui.ProtocolVersion))
			return
		}
		if input.ThreadID == "" || input.RunID == "" {
			httpError(w, 400, fmt.Errorf("threadId and runId must be nonempty"))
			return
		}
		messageIDs := map[string]bool{}
		for _, message := range input.Messages {
			if message.ID == "" || messageIDs[message.ID] {
				httpError(w, 400, fmt.Errorf("message IDs must be nonempty and unique"))
				return
			}
			messageIDs[message.ID] = true
		}
		userID := resolveQueryUserID(w, r, "", authCfg)
		if userID == "" {
			httpError(w, 401, fmt.Errorf("authorization required"))
			return
		}
		ctx := r.Context()
		if len(bindings) > 0 {
			ctx = WithAGUIWorkspaceBindings(ctx, bindings[0])
		}
		if iauth.EffectiveUserID(ctx) == "" {
			ctx = iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: userID})
		}
		if _, ok := w.(http.Flusher); !ok {
			httpError(w, 500, fmt.Errorf("streaming response unavailable"))
			return
		}
		var props struct {
			Agently *agui.Extension `json:"agently"`
		}
		if len(input.ForwardedProps) > 0 {
			_ = json.Unmarshal(input.ForwardedProps, &props)
		}
		if props.Agently != nil && props.Agently.Operation == "run.attach" {
			serveAGUIAttachment(w, r.WithContext(ctx), &input, userID, client, runtime, authCfg, bindings...)
			return
		}
		var proxy *MCPAppsProxyRequest
		if forwarded := bytes.TrimSpace(input.ForwardedProps); len(forwarded) > 0 && forwarded[0] == '{' {
			parsedProxy, hasProxy, proxyErr := ParseMCPAppsProxyRequest(input.ForwardedProps)
			if proxyErr != nil {
				httpError(w, 400, proxyErr)
				return
			}
			if hasProxy {
				proxy = parsedProxy
			}
		}
		goalCommand := props.Agently != nil && strings.HasPrefix(props.Agently.Operation, "goal.")
		stateCommand := props.Agently != nil && strings.HasPrefix(props.Agently.Operation, "state.")
		approvalCommand := props.Agently != nil && strings.HasPrefix(props.Agently.Operation, "approval.")
		conversationCommand := props.Agently != nil && strings.HasPrefix(props.Agently.Operation, "conversation.")
		workspaceCommand := props.Agently != nil && (strings.HasPrefix(props.Agently.Operation, "workspace.") || strings.HasPrefix(props.Agently.Operation, "datasource.") || strings.HasPrefix(props.Agently.Operation, "lookup.") || strings.HasPrefix(props.Agently.Operation, "feed.") || strings.HasPrefix(props.Agently.Operation, "run."))
		resourceCommand := goalCommand || stateCommand || workspaceCommand || conversationCommand || approvalCommand
		if !resourceCommand && proxy == nil && len(input.ForwardedProps) > 0 {
			var forwarded map[string]json.RawMessage
			if json.Unmarshal(input.ForwardedProps, &forwarded) == nil {
				if envelope, exists := forwarded["agently"]; exists {
					if err = extensions.ValidateExecutionEnvelope(envelope); err != nil {
						httpError(w, 400, fmt.Errorf("invalid execution selection: %w", err))
						return
					}
				}
			}
		}
		var commandPayload json.RawMessage
		if resourceCommand {
			var forwarded map[string]json.RawMessage
			if err = json.Unmarshal(input.ForwardedProps, &forwarded); err != nil {
				httpError(w, 400, err)
				return
			}
			if goalCommand {
				err = extensions.ValidateGoalEnvelope(forwarded["agently"])
			} else if stateCommand {
				err = extensions.ValidateStateEnvelope(forwarded["agently"])
			} else if approvalCommand {
				err = extensions.ValidateApprovalEnvelope(forwarded["agently"])
			} else if conversationCommand {
				err = extensions.ValidateConversationEnvelope(forwarded["agently"])
			}
			if err != nil {
				httpError(w, 400, err)
				return
			}
			var envelope struct {
				Payload json.RawMessage `json:"payload"`
				Target  *struct {
					ThreadID string `json:"threadId"`
				} `json:"target"`
			}
			_ = json.Unmarshal(forwarded["agently"], &envelope)
			if envelope.Target != nil && envelope.Target.ThreadID != input.ThreadID {
				httpError(w, 400, fmt.Errorf("resource target must match outer threadId"))
				return
			}
			commandPayload = envelope.Payload
			if approvalCommand {
				if err = extensions.ValidateApprovalPayload(props.Agently.Operation, commandPayload); err != nil {
					httpError(w, 400, err)
					return
				}
			}
			if conversationCommand {
				if err = extensions.ValidateConversationPayload(props.Agently.Operation, commandPayload); err != nil {
					httpError(w, 400, err)
					return
				}
			}
			if workspaceCommand {
				if strings.HasPrefix(props.Agently.Operation, "run.") {
					err = extensions.ValidateRunEnvelope(forwarded["agently"])
					if err == nil {
						err = extensions.ValidateRunPayload(props.Agently.Operation, commandPayload)
					}
				} else {
					err = extensions.ValidateWorkspaceEnvelope(forwarded["agently"])
					if err == nil {
						err = extensions.ValidateWorkspacePayload(props.Agently.Operation, commandPayload)
					}
				}
				if err != nil {
					httpError(w, 400, err)
					return
				}
			}
		}
		if props.Agently != nil && (props.Agently.Version != "1" || (props.Agently.Operation != "chat" && props.Agently.Operation != "capabilities" && !resourceCommand)) {
			httpError(w, 400, fmt.Errorf("unsupported Agently extension operation or version"))
			return
		}
		if props.Agently != nil && props.Agently.Operation == "capabilities" {
			beginAGUIStream(w)
			tr := agui.NewTranslator(input.ThreadID, input.RunID)
			writeAGUIEvents(w, tr.Start())
			writeAGUIEvents(w, []agui.Event{aguiDurableCapabilities(ctx, client)})
			writeAGUIEvents(w, tr.Finish("success"))
			return
		}
		authorizedThreadID := input.ThreadID
		if proxy != nil {
			if len(input.Messages) != 0 || len(input.Tools) != 0 || props.Agently != nil {
				httpError(w, 400, fmt.Errorf("MCP proxy cannot include model messages, tools or commands"))
				return
			}
			app, resolveErr := ResolveAGUIMCPApp(ctx, store, userID, proxy.ServerID, proxy.ServerHash)
			if resolveErr != nil {
				httpError(w, 403, resolveErr)
				return
			}
			var host *AGUIMCPAppsHost
			if len(bindings) > 0 {
				host = bindings[0].MCPApps
			}
			if host == nil {
				if provider, ok := client.(interface{ aguiMCPAppsHost() *AGUIMCPAppsHost }); ok {
					host = provider.aguiMCPAppsHost()
				}
			}
			if host == nil {
				httpError(w, http.StatusNotImplemented, fmt.Errorf("MCP Apps host binding unavailable"))
				return
			}
			binding := host.Bind(ctx, *app)
			if binding.Authorize == nil {
				httpError(w, 403, fmt.Errorf("MCP Apps app authorization unavailable"))
				return
			}
			if err = binding.Authorize(ctx, *app); err != nil {
				httpError(w, 403, err)
				return
			}
			ctx = WithAGUIMCPAppsBindings(ctx, binding)
			authorizedThreadID = app.ThreadID
		}
		// Store binding owns exact-byte native identity and foreign-owner checks.
		// Do not normalize or SQL-resolve an unknown public thread as a native ID.
		if conversationCommand || proxy != nil {
			boundThread, threadErr := store.GetThread(ctx, userID, authorizedThreadID)
			if threadErr != nil && !errors.Is(threadErr, aguistore.ErrNotFound) {
				httpError(w, 403, fmt.Errorf("thread unavailable"))
				return
			}
			nativeConversationID := ""
			if boundThread != nil {
				nativeConversationID = boundThread.ConversationID
			}
			if nativeConversationID == "" && (conversationCommand || proxy != nil) {
				nativeConversationID = authorizedThreadID
			}
			if nativeConversationID != "" {
				conv, readErr := client.GetConversation(ctx, nativeConversationID)
				if readErr != nil || conv == nil || conv.Id != nativeConversationID || conv.CreatedByUserId == nil || *conv.CreatedByUserId != userID {
					httpError(w, 403, fmt.Errorf("original conversation unavailable"))
					return
				}
			}
		}
		after, err := parseAGUICursor(r.Header.Get("Last-Event-ID"), input.ThreadID, input.RunID)
		if err != nil {
			httpError(w, 400, err)
			return
		}
		var browserRegistry *browsermcp.Registry
		if len(bindings) > 0 {
			browserRegistry = bindings[0].BrowserMCP
		}
		verifiedBrowserTools, browserErr := browserRegistry.Validate(ctx, userID, input.ThreadID, input.Tools)
		if browserErr != nil {
			httpError(w, 403, browserErr)
			return
		}
		ctx = browsermcp.WithDefinitions(ctx, verifiedBrowserTools)
		session, err := aguiClientToolSession(&input)
		if err != nil {
			httpError(w, 400, err)
			return
		}
		admission := aguistore.Admission{ThreadID: input.ThreadID, RunID: input.RunID, Principal: userID, ParentRunID: input.ParentRunID, Input: body}
		existing, lookupErr := store.GetRun(ctx, userID, input.ThreadID, input.RunID)
		if lookupErr != nil && !errors.Is(lookupErr, aguistore.ErrNotFound) {
			httpError(w, 500, lookupErr)
			return
		}
		if (existing == nil && after > 0) || (existing != nil && after > existing.LastSequence) {
			httpError(w, 400, fmt.Errorf("Last-Event-ID exceeds this run's journal"))
			return
		}
		var prior *aguistore.Run
		var pending aguiPending
		if existing != nil {
			admission.TurnID = existing.TurnID
			admission.ClientMessageID = existing.ClientMessageID
			admission.PriorRunID = existing.PriorRunID
			if existing.PriorRunID != "" {
				original, readErr := store.GetRun(ctx, userID, input.ThreadID, existing.PriorRunID)
				if readErr != nil {
					httpError(w, 500, readErr)
					return
				}
				var originalPending aguiPending
				if err = json.Unmarshal(original.Pending, &originalPending); err != nil {
					httpError(w, 500, err)
					return
				}
				if coordinator, ok := client.(interface {
					aguiCompleteResumeWithApprovalReceipts(context.Context, *aguistore.Run, *agui.RunAgentInput, aguiPending) error
				}); ok {
					if err = coordinator.aguiCompleteResumeWithApprovalReceipts(ctx, original, &input, originalPending); err != nil {
						httpError(w, 409, err)
						return
					}
					admission.Input = rawAGUI(&input)
				}
			}
		} else if resourceCommand || proxy != nil && len(input.Resume) == 0 {
			// A resource command owns a protocol run, not a model execution turn.
			if proxy != nil {
				admission.TurnID = uuid.NewString()
			}
		} else if len(input.Resume) > 0 || len(input.Messages) == 0 || input.Messages[len(input.Messages)-1].Role != "user" {
			prior, pending, err = findAGUIContinuation(ctx, store, userID, &input, client)
			if err != nil {
				httpError(w, 409, err)
				return
			}
			resumeInput := input
			resumeInput.ThreadID = prior.ConversationID
			if proxy != nil {
				resumeInput.ThreadID = authorizedThreadID
				priorProxy, handled, parseErr := func() (*MCPAppsProxyRequest, bool, error) {
					var original agui.RunAgentInput
					if e := json.Unmarshal(prior.Input, &original); e != nil {
						return nil, false, e
					}
					return ParseMCPAppsProxyRequest(original.ForwardedProps)
				}()
				if parseErr != nil || !handled || !bytes.Equal(rawAGUI(priorProxy), rawAGUI(proxy)) {
					httpError(w, 409, fmt.Errorf("MCP proxy continuation differs from original request"))
					return
				}
			}
			if err = preflightAGUIResume(ctx, client, &resumeInput, pending); err != nil {
				httpError(w, 409, err)
				return
			}
			admission.PriorRunID, admission.ExpectedPriorRevision, admission.TurnID = prior.RunID, prior.Revision, prior.TurnID
			admission.Input = rawAGUI(&input)
		} else {
			admission.TurnID = uuid.NewString()
			admission.ClientMessageID = input.Messages[len(input.Messages)-1].ID
		}
		var query *agentsvc.QueryInput
		if existing == nil && !resourceCommand && proxy == nil {
			query, err = queryForAGUI(&input, userID, admission.TurnID, props.Agently, prior)
			if err != nil {
				httpError(w, 400, err)
				return
			}
		}
		record, fresh, err := store.Admit(ctx, admission)
		if err != nil {
			status := 500
			if errors.Is(err, aguistore.ErrConflict) {
				status = 409
			}
			if errors.Is(err, aguistore.ErrNotFound) {
				status = http.StatusForbidden
			}
			httpError(w, status, err)
			return
		}
		if fresh && proxy == nil && !resourceCommand {
			notifyAGUIRunUpdated(ctx, client, record.ConversationID)
		}
		if proxy != nil && (fresh || record.Status == aguistore.StatusAdmitted) {
			if record.PriorRunID != "" && prior == nil {
				prior, err = store.GetRun(ctx, userID, input.ThreadID, record.PriorRunID)
				if err != nil {
					httpError(w, 500, err)
					return
				}
			}
			if record.TurnID == "" {
				record.TurnID = uuid.NewString()
			}
			go func() {
				if e := runAGUIMCPProxyWorker(context.WithoutCancel(ctx), client, store, record, &input, proxy, prior); e != nil {
					log.Printf("AG-UI MCP proxy worker: %v", e)
				}
			}()
		} else if resourceCommand && (fresh || record.Status == aguistore.StatusAdmitted) {
			go runAGUIResourceWorker(context.WithoutCancel(ctx), client, store, record, props.Agently.Operation, commandPayload)
		} else if fresh || record.Status == aguistore.StatusAdmitted {
			if record.PriorRunID != "" && prior == nil {
				prior, err = store.GetRun(ctx, userID, input.ThreadID, record.PriorRunID)
				if err != nil {
					httpError(w, 500, err)
					return
				}
				if err = json.Unmarshal(prior.Pending, &pending); err != nil {
					httpError(w, 500, err)
					return
				}
			}
			if query == nil {
				query, err = queryForAGUI(&input, userID, record.TurnID, props.Agently, prior)
				if err != nil {
					httpError(w, 400, err)
					return
				}
			}
			if err = bindAGUINativeQuery(record, query); err != nil {
				httpError(w, 500, err)
				return
			}
			workerCtx := clienttool.WithSession(context.WithoutCancel(ctx), session)
			go func() {
				if err := runAGUIDurable(workerCtx, client, runtime, store, record, &input, query, pending, prior); err != nil {
					log.Printf("AG-UI worker failed thread=%s run=%s: %v", record.ThreadID, record.RunID, err)
				}
			}()
		}
		beginAGUIStream(w)
		nextRecovery := time.Time{}
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		keepalive := time.NewTicker(streamKeepaliveInterval)
		defer keepalive.Stop()
		for {
			events, err := store.Replay(ctx, userID, input.ThreadID, input.RunID, after, 256)
			if err != nil {
				return
			}
			for _, event := range events {
				if _, err = fmt.Fprintf(w, "id: %s\ndata: %s\n\n", aguiCursor(input.ThreadID, input.RunID, event.Sequence), event.Event); err != nil {
					return
				}
				after = event.Sequence
			}
			if len(events) > 0 {
				w.(http.Flusher).Flush()
				continue
			}
			latest, err := store.GetRun(ctx, userID, input.ThreadID, input.RunID)
			if err != nil {
				return
			}
			if aguiRunTerminal(latest.Status) && after >= latest.LastSequence {
				return
			}
			if latest.Status == aguistore.StatusRunning && (latest.LeaseUntil == nil || !latest.LeaseUntil.After(time.Now())) && time.Now().After(nextRecovery) {
				nextRecovery = time.Now().Add(time.Second)
				recoveryCtx := context.WithoutCancel(ctx)
				if proxy != nil {
					var recoveryPrior *aguistore.Run
					if latest.PriorRunID != "" {
						recoveryPrior, _ = store.GetRun(ctx, userID, input.ThreadID, latest.PriorRunID)
					}
					go func(record *aguistore.Run) {
						if e := runAGUIMCPProxyWorker(recoveryCtx, client, store, record, &input, proxy, recoveryPrior); e != nil && !errors.Is(e, aguistore.ErrConflict) {
							log.Printf("AG-UI MCP proxy recovery: %v", e)
						}
					}(latest)
				} else if resourceCommand {
					go runAGUIResourceWorker(recoveryCtx, client, store, latest, props.Agently.Operation, commandPayload)
				} else if !resourceCommand {
					go func(record *aguistore.Run) {
						if err := recoverAGUIDurable(recoveryCtx, client, runtime, store, record); err != nil && !errors.Is(err, aguistore.ErrConflict) {
							log.Printf("AG-UI observation recovery failed: %v", err)
						}
					}(latest)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-keepalive.C:
				if _, err = io.WriteString(w, ": keepalive\n\n"); err != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
		}
	}
}

func aguiRunTerminal(status string) bool {
	return status == aguistore.StatusFinished || status == aguistore.StatusInterrupted || status == aguistore.StatusError || status == aguistore.StatusCancelled
}
func aguiCursor(threadID, runID string, sequence int64) string {
	b, _ := json.Marshal([]string{threadID, runID, strconv.FormatInt(sequence, 10)})
	return base64.RawURLEncoding.EncodeToString(b)
}
func parseAGUICursor(cursor, threadID, runID string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("invalid replay cursor")
	}
	var parts []string
	if json.Unmarshal(b, &parts) != nil || len(parts) != 3 || parts[0] != threadID || parts[1] != runID {
		return 0, fmt.Errorf("replay cursor belongs to another run")
	}
	n, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid replay sequence")
	}
	return n, nil
}

func findAGUIContinuation(ctx context.Context, store aguistore.Store, userID string, input *agui.RunAgentInput, clients ...Client) (*aguistore.Run, aguiPending, error) {
	runs, err := store.ListPending(ctx, userID, input.ThreadID)
	if err != nil {
		return nil, aguiPending{}, err
	}
	var resumes []agui.WireResumeEntry
	if len(input.Resume) > 0 {
		if err = json.Unmarshal(input.Resume, &resumes); err != nil {
			return nil, aguiPending{}, err
		}
	}
	requested := map[string]bool{}
	for _, resume := range resumes {
		requested[resume.InterruptId] = true
	}
	for _, message := range input.Messages {
		if message.Role == "tool" {
			requested[message.ToolCallID] = true
		}
	}
	var match *aguistore.Run
	var selected aguiPending
	for _, run := range runs {
		var pending aguiPending
		if json.Unmarshal(run.Pending, &pending) != nil {
			continue
		}
		found := false
		for _, call := range pending.ClientTools {
			if requested[aguiPendingCallID(call)] {
				found = true
			}
		}
		for _, interrupt := range pending.Interrupts {
			if requested[interrupt.ID] {
				found = true
			}
		}
		if found {
			if match != nil {
				return nil, selected, fmt.Errorf("continuation matches more than one pending run")
			}
			match, selected = run, pending
		}
	}
	if match == nil {
		return nil, selected, fmt.Errorf("no pending continuation matches supplied answers")
	}
	// Completed server approval receipts supply genuine answers to mixed resumes.
	// No unresolved elicitation or frontend tool answer is manufactured.
	if len(clients) > 0 {
		if coordinator, ok := clients[0].(interface {
			aguiCompleteResumeWithApprovalReceipts(context.Context, *aguistore.Run, *agui.RunAgentInput, aguiPending) error
		}); ok {
			if err := coordinator.aguiCompleteResumeWithApprovalReceipts(ctx, match, input, selected); err != nil {
				return nil, selected, err
			}
			var completed []agui.WireResumeEntry
			if len(input.Resume) > 0 {
				if err := json.Unmarshal(input.Resume, &completed); err != nil {
					return nil, selected, err
				}
			}
			for _, answer := range completed {
				requested[answer.InterruptId] = true
			}
		}
	}
	// All answers must be genuinely available before admitting a successor.
	for _, call := range selected.ClientTools {
		if !requested[aguiPendingCallID(call)] {
			return nil, selected, fmt.Errorf("missing client tool result %q", aguiPendingCallID(call))
		}
	}
	for _, interrupt := range selected.Interrupts {
		if !requested[interrupt.ID] {
			return nil, selected, fmt.Errorf("missing interrupt answer %q", interrupt.ID)
		}
	}
	return match, selected, nil
}

func queryForAGUI(input *agui.RunAgentInput, userID, turnID string, extension *agui.Extension, prior *aguistore.Run) (*agentsvc.QueryInput, error) {
	query := &agentsvc.QueryInput{ConversationID: input.ThreadID, MessageID: turnID, UserId: userID, ElicitationMode: "deferred"}
	if prior == nil {
		if len(input.Messages) == 0 {
			return nil, fmt.Errorf("new run requires user content")
		}
		last := input.Messages[len(input.Messages)-1]
		if last.Role != "user" {
			return nil, fmt.Errorf("new run requires a user message")
		}
		if err := json.Unmarshal(last.Content, &query.Query); err != nil {
			items, err := clienttool.MapContent(last.Content)
			if err != nil {
				return nil, err
			}
			query.ContentItems = items
			query.Query = clienttool.ContentText(items)
		}
		query.DisplayQuery = query.Query
	}
	query.Context = map[string]any{"agui": map[string]any{"context": input.Context, "state": json.RawMessage(input.State), "forwardedProps": json.RawMessage(input.ForwardedProps)}}
	if err := applyAGUIExecution(query, extension, prior != nil); err != nil {
		return nil, err
	}
	return query, nil
}

type aguiJournalWriter struct {
	client          Client
	ctx             context.Context
	store           aguistore.Store
	run             *aguistore.Run
	projection      *aguistate.Projection
	threadRevision  int64
	leaseOwner      string
	messageBaseline json.RawMessage
	stateBaseline   json.RawMessage
}

func (w *aguiJournalWriter) write(raw []json.RawMessage, pending *aguiPending) error {
	if len(w.stateBaseline) == 0 {
		w.stateBaseline = rawAGUI(w.projection.State)
	}
	intentionalState, classifyErr := aguiIntentionalStateWrite(raw, w.stateBaseline)
	if classifyErr != nil {
		return classifyErr
	}
	for attempt := 0; attempt < 4; attempt++ {
		err := w.writeOnce(raw, pending)
		if !errors.Is(err, aguistore.ErrConflict) {
			return err
		}
		// Retry thread-only admission/message revisions. An intentional state
		// write remains fenced against a genuinely different shared state.
		current, readErr := w.store.GetRun(w.ctx, w.run.Principal, w.run.ThreadID, w.run.RunID)
		if readErr != nil {
			return readErr
		}
		if current.Revision != w.run.Revision || current.LeaseOwner != w.leaseOwner {
			return err
		}
		if intentionalState {
			thread, readErr := w.store.GetThread(w.ctx, w.run.Principal, w.run.ThreadID)
			if readErr != nil {
				return readErr
			}
			same, compareErr := aguistate.EqualJSON(w.stateBaseline, thread.State)
			if compareErr != nil {
				return compareErr
			}
			if !same {
				return err
			}
		}
	}
	return aguistore.ErrConflict
}
func (w *aguiJournalWriter) writeOnce(raw []json.RawMessage, pending *aguiPending) error {
	if len(raw) == 0 && pending == nil {
		return nil
	}
	before, _ := json.Marshal(w.projection)
	committed := false
	defer func() {
		if !committed {
			// JSON decoding into existing maps preserves keys absent in the saved
			// value. Reconstruct every lane so a rolled-back START cannot survive.
			var restored aguistate.Projection
			if decodeAGUIValue(before, &restored) == nil {
				*w.projection = restored
			}
		}
	}()
	thread, err := w.store.GetThread(w.ctx, w.run.Principal, w.run.ThreadID)
	if err != nil {
		return err
	}
	var latest []aguistate.Object
	if err = decodeAGUIValue(thread.Messages, &latest); err != nil {
		return err
	}
	var baseline []aguistate.Object
	baselineJSON := w.messageBaseline
	if len(baselineJSON) == 0 {
		baselineJSON = thread.Messages
	}
	if err = decodeAGUIValue(baselineJSON, &baseline); err != nil {
		return err
	}
	w.threadRevision = thread.Revision
	intentionalState, err := aguiIntentionalStateWrite(raw, w.stateBaseline)
	if err != nil {
		return err
	}
	if intentionalState {
		same, compareErr := aguistate.EqualJSON(w.stateBaseline, thread.State)
		if compareErr != nil {
			return compareErr
		}
		if !same {
			return fmt.Errorf("%w: shared state changed concurrently", aguistore.ErrConflict)
		}
	}
	var events []json.RawMessage
	stateChanged := false
	for _, event := range raw {
		var header struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(event, &header)
		if header.Type == "STATE_SNAPSHOT" || header.Type == "STATE_DELTA" {
			stateChanged = true
		}
		if header.Type == "STATE_SNAPSHOT" && !intentionalState {
			event, err = aguiEchoSnapshot(event, thread.State)
			if err != nil {
				return err
			}
		}
		event, err = preserveMCPAppSnapshotAliases(event, w.projection.Messages)
		if err != nil {
			return err
		}
		event, err = registerMCPAppActivity(event, w.run, w.run.LastSequence+int64(len(events))+1)
		if err != nil {
			return err
		}
		if err = validateMCPAppJournalAncestry(w.ctx, w.store, w.run, event, w.run.LastSequence+int64(len(events))+1); err != nil {
			return err
		}
		normalized, err := w.projection.Apply(event)
		if err != nil {
			return err
		}
		merged, mergeErr := aguiChangedMessages(baseline, w.projection.Messages, latest)
		if mergeErr != nil {
			return mergeErr
		}
		for _, item := range normalized {
			canonical, canonicalErr := aguiCanonicalSnapshot(item, merged)
			if canonicalErr != nil {
				return canonicalErr
			}
			events = append(events, canonical)
		}
	}
	state, _, err := w.projection.Snapshot()
	if err != nil {
		return err
	}
	merged, err := aguiChangedMessages(baseline, w.projection.Messages, latest)
	if err != nil {
		return err
	}
	messages := rawAGUI(merged)
	change := &aguistore.Change{Messages: messages, ExpectedThreadRevision: w.threadRevision, LeaseOwner: w.leaseOwner}
	if stateChanged {
		change.State = state
	}
	if pending != nil {
		change.Pending, err = json.Marshal(pending)
		if err != nil {
			return err
		}
	}
	next, err := w.store.Append(w.ctx, w.run.Principal, w.run.ThreadID, w.run.RunID, w.run.Revision, events, change)
	if err != nil {
		return err
	}
	becameTerminal := !aguiRunTerminal(w.run.Status) && aguiRunTerminal(next.Status)
	w.run = next
	w.messageBaseline = rawAGUI(w.projection.Messages)
	w.stateBaseline = rawAGUI(w.projection.State)
	w.threadRevision++
	committed = true
	if becameTerminal && next.TurnID != "" {
		notifyAGUIRunUpdated(w.ctx, w.client, next.ConversationID)
	}
	return nil
}

func mergeAGUIMessages(existing, incoming []aguistate.Object) []aguistate.Object {
	index := map[string]int{}
	result := append(make([]aguistate.Object, 0, len(existing)+len(incoming)), existing...)
	for i, m := range result {
		if id, ok := m["id"].(string); ok {
			index[id] = i
		}
	}
	for _, m := range incoming {
		id, _ := m["id"].(string)
		if i, ok := index[id]; ok {
			result[i] = m
		} else {
			index[id] = len(result)
			result = append(result, m)
		}
	}
	return result
}

func (w *aguiJournalWriter) fail(err error) error {
	var events []json.RawMessage
	for id, owner := range w.projection.Text {
		event := map[string]any{"type": "TEXT_MESSAGE_END", "messageId": id}
		for _, m := range w.projection.Messages {
			if m["id"] == id && m["role"] == "reasoning" {
				event["type"] = "REASONING_MESSAGE_END"
			}
		}
		if owner != "" {
			event["subagentRunId"] = owner
		}
		events = append(events, rawAGUI(event))
	}
	for id, owner := range w.projection.Tools {
		event := map[string]any{"type": "TOOL_CALL_END", "toolCallId": id}
		if owner != "" {
			event["subagentRunId"] = owner
		}
		events = append(events, rawAGUI(event))
	}
	events = append(events, rawAGUI(map[string]any{"type": "RUN_ERROR", "message": err.Error(), "code": "EXECUTION_FAILED"}))
	return w.write(events, nil)
}
func encodeAGUIEvents(events []agui.Event) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(events))
	for _, event := range events {
		b, _ := json.Marshal(event)
		out = append(out, b)
	}
	return out
}
func rawAGUI(value any) json.RawMessage { b, _ := json.Marshal(value); return b }
func decodeAGUIValue(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(value)
}

func runAGUIDurable(ctx context.Context, client Client, runtime aguiRuntime, store aguistore.Store, record *aguistore.Run, input *agui.RunAgentInput, query *agentsvc.QueryInput, pending aguiPending, prior *aguistore.Run) (retErr error) {
	if err := bindAGUINativeQuery(record, query); err != nil {
		return err
	}
	expectedConversationID := record.ConversationID
	owner := uuid.NewString()
	claimed, err := store.Claim(ctx, record.Principal, record.ThreadID, record.RunID, record.Revision, owner, time.Minute)
	if err != nil {
		return fmt.Errorf("claim run observer: %w", err)
	}
	if claimed == nil || claimed.ConversationID != expectedConversationID {
		return fmt.Errorf("claimed native conversation identity changed")
	}
	record = claimed
	initialized := false
	defer func() {
		if retErr != nil && !initialized {
			if journalErr := journalAGUIInitializationFailure(ctx, client, store, claimed, retErr); journalErr != nil {
				log.Printf("AG-UI initialization failure could not be journaled thread=%s run=%s: %v", claimed.ThreadID, claimed.RunID, journalErr)
			}
		}
	}()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopRenew := make(chan struct{})
	defer close(stopRenew)
	go func() {
		revision := record.LeaseRevision
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopRenew:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				updated, err := store.Renew(ctx, record.Principal, record.ThreadID, record.RunID, revision, owner, time.Minute)
				if err != nil {
					cancel()
					return
				}
				revision = updated.LeaseRevision
			}
		}
	}()
	thread, err := store.GetThread(ctx, record.Principal, record.ThreadID)
	if err != nil {
		return err
	}
	projection, err := aguistate.New(thread.State, thread.Messages)
	if err != nil {
		return err
	}
	if prior == nil {
		var user aguistate.Object
		var original struct {
			Messages []json.RawMessage `json:"messages"`
		}
		if err = json.Unmarshal(record.Input, &original); err != nil {
			return err
		}
		if len(original.Messages) == 0 {
			return fmt.Errorf("accepted input has no new message")
		}
		if err = decodeAGUIValue(original.Messages[len(original.Messages)-1], &user); err != nil {
			return err
		}
		seedAGUIUserIdentity(user, record)
		projection.Messages = append(projection.Messages, user)
	} else {
		var original struct {
			Messages []json.RawMessage `json:"messages"`
		}
		if err = json.Unmarshal(record.Input, &original); err != nil {
			return err
		}
		for _, call := range pending.ClientTools {
			for _, message := range original.Messages {
				var value aguistate.Object
				if err = decodeAGUIValue(message, &value); err != nil {
					return err
				}
				if value["role"] == "tool" && value["toolCallId"] == aguiPendingCallID(call) {
					projection.Messages = mergeAGUIMessages(projection.Messages, []aguistate.Object{value})
					break
				}
			}
		}
	}
	writer := &aguiJournalWriter{client: client, ctx: ctx, store: store, run: record, projection: projection, threadRevision: thread.Revision, leaseOwner: owner, messageBaseline: append(json.RawMessage(nil), thread.Messages...)}
	translator := agui.NewTranslator(record.ThreadID, record.RunID)
	if err = translator.SetNativeIdentity(record.TurnID); err != nil {
		return err
	}
	var initialMessages []json.RawMessage
	if err = json.Unmarshal(rawAGUI(projection.Messages), &initialMessages); err != nil {
		return err
	}
	if err = translator.SeedMessages(initialMessages); err != nil {
		return err
	}
	start := encodeAGUIEvents(translator.Start())
	if input.ParentRunID != "" {
		var opening map[string]json.RawMessage
		_ = json.Unmarshal(start[0], &opening)
		opening["parentRunId"] = rawAGUI(input.ParentRunID)
		start[0] = rawAGUI(opening)
	}
	if err = writer.write(start, nil); err != nil {
		return fmt.Errorf("start run journal: %w", err)
	}
	defer func() {
		if retErr != nil && !errors.Is(retErr, context.Canceled) && !errors.Is(retErr, context.DeadlineExceeded) && !errors.Is(retErr, aguistore.ErrConflict) && !errors.Is(retErr, errAGUIObserverLost) && !aguiRunTerminal(writer.run.Status) {
			_ = writer.fail(retErr)
		}
	}()
	if prior != nil {
		if err = restoreAGUIContinuationControls(ctx, store, record, query); err != nil {
			return err
		}
	}
	state := projection.State
	if len(input.State) > 0 && !aguiUsesServerState(input) {
		if err = decodeAGUIValue(input.State, &state); err != nil {
			return err
		}
	}
	if err = writer.write([]json.RawMessage{rawAGUI(map[string]any{"type": "STATE_SNAPSHOT", "snapshot": state}), rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": projection.Messages})}, nil); err != nil {
		return fmt.Errorf("initialize run snapshots: %w", err)
	}
	initialized = true
	return executeAGUIRuntime(ctx, client, runtime, writer, translator, input, query, pending, prior)
}

// executeAGUIRuntime is shared by a new admitted worker and recovery after a
// committed RUN_STARTED but before native execution was admitted. The caller
// owns the observer lease, initial journal, projection, and heartbeat.
func executeAGUIRuntime(ctx context.Context, client Client, runtime aguiRuntime, writer *aguiJournalWriter, translator *agui.Translator, input *agui.RunAgentInput, query *agentsvc.QueryInput, pending aguiPending, prior *aguistore.Run) error {
	record := writer.run
	if err := bindAGUINativeQuery(record, query); err != nil {
		return err
	}
	if producer, ok := client.(interface {
		aguiPublishMCPApp(context.Context, mcpapps.AppBinding, json.RawMessage, json.RawMessage) error
	}); ok {
		ctx = mcpapps.WithActivityEmitter(ctx, producer.aguiPublishMCPApp)
	}
	var err error
	if query.Context == nil {
		query.Context = map[string]any{}
	}
	protocolContext, ok := query.Context["agui"].(map[string]any)
	if !ok {
		protocolContext = map[string]any{}
		query.Context["agui"] = protocolContext
	}
	protocolContext["state"] = rawAGUI(writer.projection.State)
	filter := func(e *streaming.Event) bool {
		return e != nil && (e.ConversationID == record.ConversationID || (e.ConversationID == "" && e.StreamID == record.ConversationID)) && e.TurnID == record.TurnID
	}
	sub, err := subscribeNativeEvents(ctx, client, &StreamEventsInput{ConversationID: record.ConversationID, Filter: filter})
	if err != nil {
		return err
	}
	defer sub.Close()
	observeExisting := false
	if prior != nil {
		for _, call := range pending.ClientTools {
			for _, message := range input.Messages {
				if message.Role == "tool" && message.ToolCallID == aguiPendingCallID(call) {
					if err = runtime.aguiCompleteTool(ctx, call, message.Content, valueOrEmpty(message.Error)); err != nil {
						return err
					}
					break
				}
			}
		}
		var resumes []agui.WireResumeEntry
		_ = json.Unmarshal(input.Resume, &resumes)
		for _, answer := range resumes {
			clientAnswer := false
			for _, call := range pending.ClientTools {
				if answer.InterruptId != aguiPendingCallID(call) {
					continue
				}
				content, toolError, decodeErr := aguiClientToolAnswer(call, answer)
				if decodeErr != nil {
					return decodeErr
				}
				if err = runtime.aguiCompleteTool(ctx, call, content, toolError); err != nil {
					return err
				}
				resultEvent := map[string]any{"type": "TOOL_CALL_RESULT", "messageId": call.ToolMessageID, "toolCallId": aguiPendingCallID(call), "role": "tool", "content": content}
				if answer.Metadata != nil {
					resultEvent["metadata"] = answer.Metadata
				}
				recorded, recordErr := aguiReceiptResultRecorded(ctx, writer, resultEvent)
				if recordErr != nil {
					return recordErr
				}
				if recorded {
					clientAnswer = true
					break
				}
				event := rawAGUI(resultEvent)
				wire, decodeErr := agui.DecodeEvent(event)
				if decodeErr != nil {
					return decodeErr
				}
				translated, decodeErr := translator.EmitStandard(wire)
				if decodeErr != nil {
					return decodeErr
				}
				if err = writer.write(encodeAGUIEvents(translated), nil); err != nil {
					return err
				}
				clientAnswer = true
				break
			}
			if clientAnswer {
				continue
			}
			if resolver, ok := client.(interface {
				aguiApplyInterrupt(context.Context, *aguistore.Run, agui.WireInterrupt, agui.WireResumeEntry) (aguiInterruptDisposition, error)
			}); ok {
				var interrupt *agui.WireInterrupt
				for i := range pending.Interrupts {
					if pending.Interrupts[i].ID == answer.InterruptId {
						interrupt = &pending.Interrupts[i]
						break
					}
				}
				if interrupt == nil {
					return fmt.Errorf("unknown accepted interrupt")
				}
				disposition, applyErr := resolver.aguiApplyInterrupt(ctx, record, *interrupt, answer)
				if applyErr != nil {
					return applyErr
				}
				if err = aguiProjectApprovalReceipt(ctx, runtime, writer, translator, *interrupt); err != nil {
					return err
				}
				if disposition == aguiInterruptWokeExisting {
					observeExisting = true
				}
				continue
			}
			var payload map[string]any
			if answer.Payload != nil {
				if err = json.Unmarshal(*answer.Payload, &payload); err != nil {
					return fmt.Errorf("elicitation answer requires object payload")
				}
			}
			action := "accept"
			if answer.Status == "cancelled" {
				action = "cancel"
			}
			if err = client.ResolveElicitation(ctx, &ResolveElicitationInput{ConversationID: record.ConversationID, ElicitationID: answer.InterruptId, Action: action, Payload: payload}); err != nil {
				return err
			}
		}
	}
	type result struct {
		output *agentsvc.QueryOutput
		err    error
		native *aguiRecoveredNative
	}
	observerCtx, stopObserver := context.WithCancel(ctx)
	defer stopObserver()
	observer := newAGUIInvocationObserver(observerCtx, client, record.ConversationID, record.TurnID)
	observer.runtime, observer.store, observer.run = runtime, writer.store, record
	executionCtx := requestctx.WithInvocationObserver(context.WithoutCancel(ctx), observer)
	results := make(chan result, 1)
	if observeExisting {
		results = nil
	} else {
		go func() {
			if prior == nil {
				promoter, ok := writer.store.(aguistore.ThreadPromoter)
				if !ok {
					results <- result{err: fmt.Errorf("native thread promotion unavailable")}
					return
				}
				promoted, e := promoter.PromoteThread(executionCtx, record.Principal, record.ThreadID)
				if e != nil {
					results <- result{err: e}
					return
				}
				if promoted == nil || promoted.ConversationID != record.ConversationID || promoted.ThreadID != record.ThreadID || promoted.Principal != record.Principal || promoted.ProtocolOnly {
					results <- result{err: fmt.Errorf("promoted native thread identity mismatch")}
					return
				}
				out, e := client.Query(executionCtx, query)
				results <- result{output: out, err: e}
			} else {
				out := &agentsvc.QueryOutput{}
				if len(pending.Interrupts) > 0 {
					if waiter, ok := runtime.(interface {
						aguiAwaitDeferredBoundary(context.Context, *aguistore.Run) error
					}); ok {
						waitCtx, stopWait := context.WithTimeout(ctx, 30*time.Second)
						waitErr := waiter.aguiAwaitDeferredBoundary(waitCtx, record)
						stopWait()
						if waitErr != nil {
							results <- result{output: out, err: waitErr}
							return
						}
					}
				}
				if len(pending.Interrupts) > 0 {
					if inspector, ok := runtime.(aguiRecoveryRuntime); ok {
						native, inspectErr := inspector.aguiInspectRun(ctx, record)
						if inspectErr != nil {
							results <- result{err: inspectErr}
							return
						}
						if native != nil {
							remaining := len(native.Pending.Interrupts) > 0 || len(native.Pending.ClientTools) > 0
							switch strings.ToLower(native.Status) {
							case "completed", "finished", "success", "succeeded", "failed", "error", "canceled", "cancelled":
								remaining = true
							}
							if remaining {
								results <- result{native: native}
								return
							}
						}
					}
				}
				continuationCtx, scopeErr := aguiDetachedResumeContext(executionCtx, writer.store, record)
				if scopeErr != nil {
					results <- result{output: out, err: scopeErr}
					return
				}
				var e error
				if len(pending.Dependencies) > 0 {
					if graph, ok := runtime.(interface {
						aguiResumeGraph(context.Context, *agentsvc.QueryInput, string, aguiPending, *agentsvc.QueryOutput) error
					}); ok {
						e = graph.aguiResumeGraph(continuationCtx, query, record.TurnID, pending, out)
					} else {
						e = fmt.Errorf("nested continuation requires native graph runtime")
					}
				} else {
					e = runtime.aguiResume(continuationCtx, query, record.TurnID, out)
				}
				results <- result{output: out, err: e}
			}
		}()
	}
	queued := observeExisting
	presentation := &aguiPresentation{}
	userAliases := map[string]bool{record.TurnID: true, record.ClientMessageID: true}
	identifiedUser := ""
	consume := func(e *streaming.Event) error {
		if !filter(e) {
			return nil
		}
		if e.Type == streaming.EventTypeElicitationRequested {
			if inspector, ok := runtime.(aguiRecoveryRuntime); ok {
				native, inspectErr := inspector.aguiInspectRun(ctx, record)
				if inspectErr != nil {
					return inspectErr
				}
				if native == nil || len(native.Pending.Interrupts) == 0 {
					return fmt.Errorf("native elicitation has no canonical pending descriptor")
				}
				return aguiFinishRecovered(writer, translator, native)
			}
		}
		copy := *e
		if queued && (copy.Type == streaming.EventTypeTurnCompleted || copy.Type == streaming.EventTypeTurnFailed || copy.Type == streaming.EventTypeTurnCanceled) {
			inspector, ok := runtime.(aguiRecoveryRuntime)
			if !ok {
				return fmt.Errorf("queued execution requires canonical native inspection")
			}
			native, inspectErr := inspector.aguiInspectRun(ctx, writer.run)
			if inspectErr != nil {
				return inspectErr
			}
			if native == nil {
				return fmt.Errorf("queued native execution disappeared")
			}
			if aguiNativeObservationTerminal(native) {
				return aguiFinishRecovered(writer, translator, native)
			}
			return nil // A provider/model boundary cannot finish the native turn.
		}
		if copy.Type == streaming.EventTypeTurnCompleted && !queued {
			return nil
		} // Query return owns the final pending/interrupt outcome.
		if copy.Type == streaming.EventTypeModelStarted && copy.ParentMessageID != "" {
			userAliases[copy.ParentMessageID] = true
			if identifiedUser == "" {
				if err := publishAGUIUserIdentity(writer, translator, record, copy.ParentMessageID); err != nil {
					return err
				}
				identifiedUser = copy.ParentMessageID
			}
		}
		if prior != nil && copy.ToolCallID != "" {
			for _, call := range pending.ClientTools {
				if copy.ToolCallID == call.ID {
					return nil
				}
			}
		}
		if userAliases[copy.AssistantMessageID] {
			copy.AssistantMessageID = ""
		}
		if copy.Type == streaming.EventTypeTurnQueued {
			queued = true
		}
		if err := hydrateAGUITool(ctx, client, &copy); err != nil {
			return err
		}
		var events []json.RawMessage
		for _, projected := range presentation.project(ctx, &copy, client) {
			events = append(events, encodeAGUIEvents(translator.Translate(projected))...)
		}
		return writer.write(events, nil)
	}
	nativePoll := time.NewTicker(500 * time.Millisecond)
	defer nativePoll.Stop()
	for !translator.Done() {
		select {
		case <-nativePoll.C:
			if !observeExisting && !queued {
				continue
			}
			inspector, ok := runtime.(aguiRecoveryRuntime)
			if !ok {
				return fmt.Errorf("live continuation requires native execution inspection")
			}
			native, inspectErr := inspector.aguiInspectRun(ctx, writer.run)
			if inspectErr != nil {
				return inspectErr
			}
			if native == nil {
				return fmt.Errorf("resumed live native execution disappeared")
			}
			switch strings.ToLower(native.Status) {
			case "waiting_for_user", "blocked":
				if len(native.Pending.Interrupts) == 0 && len(native.Pending.ClientTools) == 0 {
					continue
				}
				return aguiFinishRecovered(writer, translator, native)
			case "completed", "finished", "success", "succeeded", "failed", "error", "canceled", "cancelled":
				return aguiFinishRecovered(writer, translator, native)
			}
		case <-ctx.Done():
			return ctx.Err()
		case child := <-observer.events:
			if child.observerErr != nil {
				child.ack <- child.observerErr
				return child.observerErr
			}
			var translated []agui.Event
			if child.registered {
				translated = translator.RegisterInvocation(child.invocation)
			} else if child.returned != nil {
				returned := *child.returned
				for _, call := range returned.ClientToolCalls {
					if call.TurnID == "" {
						call.TurnID = child.invocation.TurnID
					}
					if translator.HasToolCall(call.TurnID, call.ID) {
						continue
					}
					event := &streaming.Event{Type: streaming.EventTypeToolCallCompleted, ConversationID: child.invocation.ConversationID, TurnID: call.TurnID, ToolCallID: call.ID, ToolName: call.Name, AssistantMessageID: call.AssistantMessageID, Arguments: call.Arguments}
					translated = append(translated, translator.TranslateSubagent(child.invocation, event)...)
				}
				if returned.Content != "" && !translator.SubagentHasText(child.invocation.ID) {
					source := &streaming.Event{Type: streaming.EventTypeModelCompleted, ConversationID: child.invocation.ConversationID, TurnID: child.invocation.TurnID, AssistantMessageID: child.invocation.ID + "/answer", Content: returned.Content}
					for _, projected := range presentation.project(ctx, source, client) {
						translated = append(translated, translator.TranslateSubagent(child.invocation, projected)...)
					}
				}
				returned.Content, _ = plainAGUIContent(returned.Content, true)
				translated = append(translated, translator.InvocationReturned(returned)...)
			} else {
				if child.event != nil && child.event.Type == streaming.EventTypeTurnCompleted {
					child.ack <- nil
					continue
				} // The real invocation return supplies its outcome.
				for _, projected := range presentation.project(ctx, child.event, client) {
					translated = append(translated, translator.TranslateSubagent(child.invocation, projected)...)
				}
			}
			err = writer.write(encodeAGUIEvents(translated), nil)
			child.ack <- err
			if err != nil {
				return err
			}
		case e, open := <-sub.C():
			if !open {
				return aguiObserverLost(sub.Reason())
			}
			if err = consume(e); err != nil {
				return err
			}
		case got := <-results:
			results = nil
			draining := true
			for draining && !translator.Done() {
				select {
				case e, open := <-sub.C():
					if !open {
						return aguiObserverLost(sub.Reason())
					}
					if err = consume(e); err != nil {
						return err
					}
				default:
					draining = false
				}
			}
			if translator.Done() {
				return nil
			}
			if got.err != nil {
				return got.err
			}
			if got.native != nil {
				return aguiFinishRecovered(writer, translator, got.native)
			}
			if queued {
				continue
			}
			if got.output == nil {
				return fmt.Errorf("agent returned no output")
			}
			nextPending := aguiPending{ClientTools: got.output.ClientToolCalls, Dependencies: got.output.ClientToolDependencies}
			if discovery, ok := client.(interface {
				aguiPendingApprovalInterrupts(context.Context, string, string) ([]agui.WireInterrupt, error)
			}); ok {
				approvals, lookupErr := discovery.aguiPendingApprovalInterrupts(ctx, record.ConversationID, record.TurnID)
				if lookupErr != nil {
					return lookupErr
				}
				for _, approval := range approvals {
					if approval.ToolCallID != nil {
						alias := agui.ProtocolToolCallID(record.TurnID, *approval.ToolCallID)
						approval.ToolCallID = &alias
					}
					nextPending.Interrupts = append(nextPending.Interrupts, approval)
				}
			}
			for i := range nextPending.ClientTools {
				call := &nextPending.ClientTools[i]
				if call.TurnID == "" {
					call.TurnID = record.TurnID
				}
				if call.ConversationID == "" {
					call.ConversationID = record.ConversationID
				}
				call.ProtocolID = agui.ProtocolToolCallID(call.TurnID, call.ID)
			}
			for _, call := range nextPending.ClientTools {
				if translator.HasToolCall(call.TurnID, call.ID) {
					continue
				}
				event := &streaming.Event{Type: streaming.EventTypeToolCallCompleted, ConversationID: call.ConversationID, TurnID: call.TurnID, ToolCallID: call.ID, ToolName: call.Name, AssistantMessageID: call.AssistantMessageID, Arguments: call.Arguments}
				if err = writer.write(encodeAGUIEvents(translator.Translate(event)), nil); err != nil {
					return err
				}
			}
			outcome := map[string]any{"type": "success"}
			if len(nextPending.ClientTools) > 0 {
				ids := make([]string, 0, len(nextPending.ClientTools))
				for _, call := range nextPending.ClientTools {
					ids = append(ids, aguiPendingCallID(call))
				}
				outcome["pendingToolCallIds"] = ids
			}
			if len(nextPending.Dependencies) > 0 && len(nextPending.ClientTools) > 0 {
				for _, call := range nextPending.ClientTools {
					nextPending.Interrupts = append(nextPending.Interrupts, aguiClientToolInterrupt(call))
				}
				outcome = map[string]any{"type": "interrupt", "interrupts": nextPending.Interrupts}
			}
			if el := got.output.Elicitation; el != nil {
				schema := rawAGUI(el.RequestedSchema)
				interrupt := agui.WireInterrupt{ID: el.ElicitationId, Reason: "elicitation"}
				text := el.Message
				interrupt.Message = &text
				if len(schema) > 0 {
					var properties map[string]json.RawMessage
					_ = json.Unmarshal(schema, &properties)
					interrupt.ResponseSchema = &properties
				}
				nextPending.Interrupts = append(nextPending.Interrupts, interrupt)
				outcome = map[string]any{"type": "interrupt", "interrupts": nextPending.Interrupts}
			}
			if len(nextPending.Interrupts) > 0 {
				for _, call := range nextPending.ClientTools {
					id := aguiPendingCallID(call)
					found := false
					for _, interrupt := range nextPending.Interrupts {
						if interrupt.ID == id {
							found = true
							break
						}
					}
					if !found {
						nextPending.Interrupts = append(nextPending.Interrupts, aguiClientToolInterrupt(call))
					}
				}
				outcome = map[string]any{"type": "interrupt", "interrupts": nextPending.Interrupts}
			}
			if got.output.ExecutionStatus == "canceled" || got.output.ExecutionStatus == "cancelled" {
				nextPending = aguiPending{}
				outcome = map[string]any{"type": "cancelled"}
			}
			if !translator.HasText() && got.output.Content != "" {
				source := &streaming.Event{Type: streaming.EventTypeAssistant, ConversationID: record.ConversationID, TurnID: record.TurnID, AssistantMessageID: record.RunID + "/answer", Content: got.output.Content, Patch: map[string]any{"role": "assistant"}}
				for _, projected := range presentation.project(ctx, source, client) {
					if err = writer.write(encodeAGUIEvents(translator.Translate(projected)), nil); err != nil {
						return err
					}
				}
			}
			terminal := encodeAGUIEvents(translator.Finish("success"))
			var end map[string]any
			_ = json.Unmarshal(terminal[len(terminal)-1], &end)
			end["outcome"] = outcome
			terminal[len(terminal)-1] = rawAGUI(end)
			return writer.write(terminal, &nextPending)
		}
	}
	return nil
}

// Older accepted runs retain their raw IDs; new runs persist the turn-scoped alias.
func aguiPendingCallID(call clienttool.PendingCall) string {
	if call.ProtocolID != "" {
		return call.ProtocolID
	}
	return call.ID
}

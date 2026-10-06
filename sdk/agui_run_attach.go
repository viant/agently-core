package sdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/protocol/agui/extensions"
	svcauth "github.com/viant/agently-core/service/auth"
)

// run.attach replays/observes an already admitted run under its original protocol
// identity. It is not a new command run or a new model turn. The accepted input
// remains private; the existing handler rechecks current native/MCP authority and
// performs its established lease recovery using that exact input.
func serveAGUIAttachment(w http.ResponseWriter, r *http.Request, input *agui.RunAgentInput, principal string, client Client, runtime aguiRuntime, authCfg *svcauth.Config, bindings ...AGUIWorkspaceBindings) {
	var forwarded map[string]json.RawMessage
	if json.Unmarshal(input.ForwardedProps, &forwarded) != nil || len(forwarded) != 1 || extensions.ValidateRunEnvelope(forwarded["agently"]) != nil {
		http.Error(w, "invalid run attachment envelope", http.StatusBadRequest)
		return
	}
	var envelope struct {
		Target *struct {
			ThreadID string `json:"threadId"`
		} `json:"target"`
	}
	_ = json.Unmarshal(forwarded["agently"], &envelope)
	var state map[string]json.RawMessage
	if len(input.Messages) != 0 || len(input.Tools) != 0 || len(input.Context) != 0 || len(input.Resume) != 0 || input.ParentRunID != "" ||
		(envelope.Target != nil && envelope.Target.ThreadID != input.ThreadID) ||
		(len(input.State) != 0 && (json.Unmarshal(input.State, &state) != nil || len(state) != 0)) {
		http.Error(w, "run attachment cannot submit messages, state, tools, context or continuation", http.StatusBadRequest)
		return
	}
	record, err := runtime.aguiStore().GetRun(r.Context(), principal, input.ThreadID, input.RunID)
	if errors.Is(err, aguistore.ErrNotFound) || err == nil && record == nil {
		http.Error(w, "run unavailable", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "run lookup failed", http.StatusInternalServerError)
		return
	}
	var original agui.RunAgentInput
	var originalProps struct {
		Agently *agui.Extension `json:"agently"`
	}
	if json.Unmarshal(record.Input, &original) != nil || original.ThreadID != input.ThreadID || original.RunID != input.RunID ||
		(len(original.ForwardedProps) > 0 && json.Unmarshal(original.ForwardedProps, &originalProps) != nil) ||
		(originalProps.Agently != nil && originalProps.Agently.Operation == "run.attach") {
		http.Error(w, "stored run cannot be attached", http.StatusConflict)
		return
	}
	request := r.Clone(r.Context())
	request.Body = io.NopCloser(bytes.NewReader(record.Input))
	request.ContentLength = int64(len(record.Input))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Del("Content-Length")
	// Reuse current authorization, immutable admission, journal ordering and
	// crash recovery. Never return the private original input to the requester.
	handleAGUIDurable(client, runtime, authCfg, bindings...)(w, request)
}

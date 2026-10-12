package sdk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	iauth "github.com/viant/agently-core/internal/auth"
	registry "github.com/viant/agently-core/internal/tool/registry"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/protocol/mcpname"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/service/browsermcp"
	"github.com/viant/mcp-protocol/schema"
)

// This runs after original-call/provenance validation and before durable
// admission. It reuses the existing MCP outputArtifacts policy/publisher, so
// neither the protocol journal nor model transcript receives extracted bytes.
func (c *backendClient) aguiNormalizeToolOutput(ctx context.Context, call clienttool.PendingCall, content json.RawMessage, toolError string) (json.RawMessage, string, error) {
	var metadata struct {
		Browser struct {
			Server   string `json:"server"`
			Tool     string `json:"tool"`
			Artifact bool   `json:"outputArtifact"`
		} `json:"browserMCP"`
	}
	if json.Unmarshal(call.Metadata, &metadata) != nil || !metadata.Browser.Artifact {
		return content, toolError, nil
	}
	fail := func() (json.RawMessage, string, error) {
		return nil, "", fmt.Errorf("browser output artifact capture unavailable")
	}
	digest := artifactAnswerDigest(content, toolError)
	failedOutput := func(reasonCodes ...string) (json.RawMessage, string, error) {
		message := "Browser output artifact capture failed; original tool call will not be replayed."
		body := map[string]any{"resources": []any{}, "sourceDigest": digest, "errorCode": "outputArtifactCaptureFailed"}
		if len(reasonCodes) == 1 && reasonCodes[0] == "outputArtifactBytesMissing" {
			body["reasonCode"] = reasonCodes[0]
			message = "Browser capture returned metadata without image or binary bytes; no artifact was published. The original tool call will not be replayed."
		}
		result, _ := json.Marshal(body)
		encoded, _ := json.Marshal(string(result))
		return encoded, message, nil
	}
	if c == nil || c.mcpMgr == nil || iauth.EffectiveUserID(ctx) == "" || call.ConversationID == "" || call.TurnID == "" || call.ID == "" || call.ToolMessageID == "" || mcpname.Canonical(call.Name) != browsermcp.ToolAlias(metadata.Browser.Server, metadata.Browser.Tool) {
		return fail()
	}
	options, err := c.mcpMgr.Options(ctx, metadata.Browser.Server)
	if err != nil || options == nil || !options.IsBrowserExecution() {
		return fail()
	}
	policy, exists := options.OutputArtifacts[metadata.Browser.Tool]
	if !exists || policy.Validate() != nil {
		return fail()
	}
	// Error results are reduced to their closed error message, not binary echoes.
	if toolError != "" {
		return failedOutput()
	}
	var raw string
	if len(content) > 12<<20 || json.Unmarshal(content, &raw) != nil {
		return failedOutput()
	}
	// The browser's negotiated complete-result projection supports the earlier
	// MCP shape without resultType. Do not invoke July's required-field decoder
	// or mistake a continuation result for a completed artifact.
	type completeResult schema.CallToolResult
	var result completeResult
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF || result.ResultType != "" && result.ResultType != "complete" {
		return failedOutput()
	}
	scope := strings.Join([]string{call.ConversationID, call.TurnID, call.ID, call.ToolMessageID, metadata.Browser.Server, metadata.Browser.Tool}, "\x00")
	captured, err := registry.CaptureOutputArtifacts(ctx, (*schema.CallToolResult)(&result), &policy, scope)
	if err != nil {
		if errors.Is(err, registry.ErrOutputArtifactBytesMissing) {
			return failedOutput("outputArtifactBytesMissing")
		}
		return failedOutput()
	}
	captured.StructuredContent.(map[string]any)["sourceDigest"] = digest
	encoded, err := json.Marshal(captured.StructuredContent)
	if err != nil {
		return fail()
	}
	normalized, err := json.Marshal(string(encoded))
	return normalized, "", err
}

func artifactAnswerDigest(content json.RawMessage, toolError string) string {
	canonical, err := canonicalJSONValue(content)
	if err != nil {
		canonical = content
	}
	raw, _ := json.Marshal([]any{string(canonical), toolError})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func artifactResultSourceDigest(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) != nil {
		return ""
	}
	var result struct {
		SourceDigest string `json:"sourceDigest"`
	}
	if json.Unmarshal([]byte(text), &result) != nil {
		return ""
	}
	if len(result.SourceDigest) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(result.SourceDigest); err != nil {
		return ""
	}
	return result.SourceDigest
}

// An already accepted retry may outlive the original input deadline. Compare
// its original provenance and source fingerprint without publishing anything;
// the idempotent publisher can then adopt the exact existing artifact.
func validateArtifactReplay(input, saved *agui.RunAgentInput, pending aguiPending) error {
	var incomingAnswers, savedAnswers []agui.WireResumeEntry
	if len(input.Resume) > 0 && json.Unmarshal(input.Resume, &incomingAnswers) != nil {
		return fmt.Errorf("invalid artifact replay")
	}
	if len(saved.Resume) > 0 && json.Unmarshal(saved.Resume, &savedAnswers) != nil {
		return fmt.Errorf("invalid accepted artifact answer")
	}
	for _, call := range pending.ClientTools {
		var meta struct {
			Browser struct {
				Artifact bool `json:"outputArtifact"`
			} `json:"browserMCP"`
		}
		if json.Unmarshal(call.Metadata, &meta) != nil || !meta.Browser.Artifact {
			continue
		}
		expected := ""
		for _, message := range saved.Messages {
			if message.Role == "tool" && message.ToolCallID == aguiPendingCallID(call) {
				expected = artifactResultSourceDigest(message.Content)
			}
		}
		for _, answer := range savedAnswers {
			if answer.InterruptId == aguiPendingCallID(call) {
				// A cancelled answer's payload is ignored by execution, but its
				// accepted projection still carries the source fingerprint.
				var payload struct {
					Content json.RawMessage `json:"content"`
				}
				if answer.Payload != nil && json.Unmarshal(*answer.Payload, &payload) == nil {
					expected = artifactResultSourceDigest(payload.Content)
				}
			}
		}
		if expected == "" {
			return fmt.Errorf("accepted artifact fingerprint unavailable")
		}
		found := false
		for _, message := range input.Messages {
			if message.Role != "tool" || message.ToolCallID != aguiPendingCallID(call) {
				continue
			}
			if found {
				return fmt.Errorf("duplicate artifact answer")
			}
			found = true
			if err := browsermcp.VerifyResultMetadata(call.Name, call.Metadata, message.Metadata); err != nil {
				return err
			}
			alreadyAccepted := false
			for _, accepted := range saved.Messages {
				if accepted.Role == "tool" && accepted.ToolCallID == message.ToolCallID {
					left, e1 := canonicalJSONValue(accepted.Content)
					right, e2 := canonicalJSONValue(message.Content)
					alreadyAccepted = e1 == nil && e2 == nil && bytes.Equal(left, right) && valueOrEmpty(accepted.Error) == valueOrEmpty(message.Error)
				}
			}
			if !alreadyAccepted && artifactAnswerDigest(message.Content, valueOrEmpty(message.Error)) != expected {
				return fmt.Errorf("artifact replay differs from accepted result")
			}
		}
		for _, answer := range incomingAnswers {
			if answer.InterruptId != aguiPendingCallID(call) {
				continue
			}
			if found {
				return fmt.Errorf("duplicate artifact answer")
			}
			found = true
			if answer.Status != "cancelled" || answer.Metadata != nil {
				if err := browsermcp.VerifyResultMetadata(call.Name, call.Metadata, rawAGUI(answer.Metadata)); err != nil {
					return err
				}
			}
			body, toolError, err := aguiClientToolAnswer(call, answer)
			if err != nil {
				return err
			}
			alreadyAccepted := false
			for _, accepted := range savedAnswers {
				if accepted.InterruptId == answer.InterruptId && accepted.Status == answer.Status && accepted.Payload != nil && answer.Payload != nil {
					left, e1 := canonicalJSONValue(*accepted.Payload)
					right, e2 := canonicalJSONValue(*answer.Payload)
					alreadyAccepted = e1 == nil && e2 == nil && bytes.Equal(left, right)
				}
			}
			if !alreadyAccepted && artifactAnswerDigest(body, toolError) != expected {
				return fmt.Errorf("artifact replay differs from accepted result")
			}
		}
		if !found {
			return fmt.Errorf("artifact replay answer missing")
		}
		// Adopt the already accepted projection. No current-policy lookup or
		// publication is needed on a retry, even if workspace policy changed.
		for index := range input.Messages {
			message := &input.Messages[index]
			if message.Role != "tool" || message.ToolCallID != aguiPendingCallID(call) {
				continue
			}
			for _, accepted := range saved.Messages {
				if accepted.Role == "tool" && accepted.ToolCallID == message.ToolCallID {
					message.Content = accepted.Content
					message.Error = accepted.Error
					break
				}
			}
		}
		for index := range incomingAnswers {
			answer := &incomingAnswers[index]
			if answer.InterruptId != aguiPendingCallID(call) {
				continue
			}
			for _, accepted := range savedAnswers {
				if accepted.InterruptId == answer.InterruptId {
					answer.Payload = accepted.Payload
					break
				}
			}
		}
	}
	if len(input.Resume) > 0 {
		encoded, err := json.Marshal(incomingAnswers)
		if err != nil {
			return err
		}
		input.Resume = encoded
	}
	return nil
}

func normalizeAGUIToolOutputs(ctx context.Context, client Client, input *agui.RunAgentInput, pending aguiPending) error {
	normalizer, ok := client.(interface {
		aguiNormalizeToolOutput(context.Context, clienttool.PendingCall, json.RawMessage, string) (json.RawMessage, string, error)
	})
	if !ok || len(pending.ClientTools) == 0 {
		return nil
	}
	for index := range input.Messages {
		message := &input.Messages[index]
		if message.Role != "tool" {
			continue
		}
		for _, call := range pending.ClientTools {
			if message.ToolCallID != aguiPendingCallID(call) {
				continue
			}
			content, normalizedError, err := normalizer.aguiNormalizeToolOutput(ctx, call, message.Content, valueOrEmpty(message.Error))
			if err != nil {
				return err
			}
			message.Content = content
			if normalizedError != "" {
				message.Error = &normalizedError
			} else {
				message.Error = nil
			}
			break
		}
	}
	if len(input.Resume) == 0 {
		return nil
	}
	var answers []agui.WireResumeEntry
	if json.Unmarshal(input.Resume, &answers) != nil {
		return fmt.Errorf("invalid client tool answers")
	}
	for index := range answers {
		answer := &answers[index]
		for _, call := range pending.ClientTools {
			if answer.InterruptId != aguiPendingCallID(call) {
				continue
			}
			content, toolError, err := aguiClientToolAnswer(call, *answer)
			if err != nil {
				return err
			}
			content, toolError, err = normalizer.aguiNormalizeToolOutput(ctx, call, content, toolError)
			if err != nil {
				return err
			}
			payload := rawAGUI(map[string]any{"content": content, "error": toolError})
			answer.Payload = &payload
			break
		}
	}
	encoded, err := json.Marshal(answers)
	if err != nil {
		return err
	}
	input.Resume = encoded
	return nil
}

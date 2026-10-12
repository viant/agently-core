package sdk

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/agui"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	scratchpad "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	"github.com/viant/agently-core/runtime/clienttool"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/service/browsermcp"
	"github.com/viant/agently-core/service/shared/toolexec"
)

type artifactHTTPClient struct {
	*durableAGUIClient
	normalizer       *backendClient
	normalizations   atomic.Int32
	nativeCompletion bool
	resumeNative     func(context.Context, *agentsvc.QueryInput, string, *agentsvc.QueryOutput) error
}

func (c *artifactHTTPClient) aguiCompleteTool(ctx context.Context, call clienttool.PendingCall, content json.RawMessage, toolError string) error {
	if !c.nativeCompletion {
		return c.durableAGUIClient.aguiCompleteTool(ctx, call, content, toolError)
	}
	c.completions.Add(1)
	if err := json.Unmarshal(content, &c.completedContent); err != nil {
		return err
	}
	return toolexec.CompleteClientToolResult(ctx, c.nativeConversation, call, content, toolError)
}

func (c *artifactHTTPClient) aguiResume(ctx context.Context, input *agentsvc.QueryInput, turnID string, output *agentsvc.QueryOutput) error {
	if c.resumeNative == nil {
		return c.durableAGUIClient.aguiResume(ctx, input, turnID, output)
	}
	c.resumes.Add(1)
	return c.resumeNative(ctx, input, turnID, output)
}

func (c *artifactHTTPClient) aguiNormalizeToolOutput(ctx context.Context, call clienttool.PendingCall, content json.RawMessage, toolError string) (json.RawMessage, string, error) {
	c.normalizations.Add(1)
	return c.normalizer.aguiNormalizeToolOutput(ctx, call, content, toolError)
}

func artifactHTTPServer(t *testing.T) (context.Context, *artifactHTTPClient, *httptest.Server, *mcpcfg.MCPClient) {
	t.Helper()
	ctx, normalizer, _, _, _, _ := browserArtifactFixture(t)
	options, e := normalizer.mcpMgr.Options(ctx, "device")
	require.NoError(t, e)
	options.OutputArtifacts = map[string]mcpcfg.OutputArtifact{"Capture/screen": {Name: "screen.png"}}
	base, old := newDurableAGUIServer(t)
	old.Close()
	client := &artifactHTTPClient{durableAGUIClient: base, normalizer: normalizer}
	handler := handleAGUIRun(client, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subject := "owner"
		if r.Header.Get("X-Fixture-User") == "foreign" {
			subject = "foreign"
		}
		handler(w, r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: subject})))
	}))
	t.Cleanup(server.Close)
	return ctx, client, server, options
}

func artifactHTTPPNG(t *testing.T) []byte {
	t.Helper()
	pixels := image.NewNRGBA(image.Rect(0, 0, 1100, 1100))
	_, _ = rand.New(rand.NewSource(37)).Read(pixels.Pix)
	for i := 3; i < len(pixels.Pix); i += 4 {
		pixels.Pix[i] = 255
	}
	var out bytes.Buffer
	require.NoError(t, png.Encode(&out, pixels))
	return out.Bytes()
}

func TestBrowserOutputArtifactHTTPBeforeAdmissionReplayAndProvenance(t *testing.T) {
	pixels := artifactHTTPPNG(t)
	encoded := base64.StdEncoding.EncodeToString(pixels)
	canonical := rawAGUI(map[string]any{"content": []any{map[string]any{"type": "text", "text": "ORIGINAL-TEXT-MARKER"}, map[string]any{"type": "image", "mimeType": "image/png", "data": encoded}}, "structuredContent": map[string]any{"duplicateMarker": "ORIGINAL-STRUCTURED-MARKER"}})
	content := rawAGUI(string(canonical))
	ctx, c, server, options := artifactHTTPServer(t)
	metadata := json.RawMessage(`{"browserMCP":{"catalogId":"original-catalog","connectionId":"original-connection","server":"device","tool":"Capture/screen","outputArtifact":true}}`)
	var originalDeadline time.Time
	c.query = func(_ context.Context, in *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		originalDeadline = time.Now().Add(10 * time.Second)
		return &agentsvc.QueryOutput{ClientToolCalls: []clienttool.PendingCall{{ID: "image-call", Name: browsermcp.ToolAlias("device", "Capture/screen"), ConversationID: in.ConversationID, TurnID: in.MessageID, AssistantMessageID: "assistant", ToolMessageID: "native-image-tool", Metadata: metadata, DeadlineAt: originalDeadline.UTC().Format(time.RFC3339Nano)}}}, nil
	}
	status, wire := durablePost(t, server, aguiChatRequest, nil)
	require.Equal(t, 200, status, wire)
	original, e := c.store.GetRun(ctx, "owner", "thread", "external-run")
	require.NoError(t, e)
	alias := agui.ProtocolToolCallID(original.TurnID, "image-call")
	input := agui.RunAgentInput{ThreadID: "thread", RunID: "image-answer", Messages: []agui.Message{{ID: "image-answer-message", Role: "tool", ToolCallID: alias, Content: content, Metadata: metadata}}}
	body := rawAGUI(input)
	require.Greater(t, len(body), 4<<20)
	require.Less(t, len(body), 16<<20)
	artifacts := func() []*scratchpad.ArtifactDescriptor {
		t.Helper()
		items, _, err := scratchpad.New().ListArtifacts(ctx, "", 100)
		require.NoError(t, err)
		return items
	}
	require.Empty(t, artifacts())
	// Fresh admission validates original provenance before any binary publication.
	missing := input
	missing.Messages = append([]agui.Message(nil), input.Messages...)
	missing.Messages[0].Metadata = nil
	status, _ = durablePost(t, server, string(rawAGUI(missing)), nil)
	require.Equal(t, 409, status)
	require.EqualValues(t, 0, c.normalizations.Load())
	require.Empty(t, artifacts())
	status, wire = durablePost(t, server, string(body), nil)
	require.Equal(t, 200, status, wire)
	assertAGUITerminal(t, decodeAGUISSE(t, bytes.NewBufferString(wire)), "RUN_FINISHED")
	require.EqualValues(t, 1, c.normalizations.Load())
	require.EqualValues(t, 1, c.completions.Load())
	require.EqualValues(t, 1, c.resumes.Load())
	require.EqualValues(t, 1, c.queries.Load())
	items := artifacts()
	require.Len(t, items, 1)
	stored, e := scratchpad.New().ReadArtifactPayload(ctx, items[0].URI)
	require.NoError(t, e)
	require.True(t, bytes.Equal(pixels, stored), "published PNG bytes changed")
	accepted, e := c.store.GetRun(ctx, "owner", "thread", "image-answer")
	require.NoError(t, e)
	thread, e := c.store.GetThread(ctx, "owner", "thread")
	require.NoError(t, e)
	events, e := c.store.Replay(ctx, "owner", "thread", "image-answer", 0, 1000)
	require.NoError(t, e)
	journal := rawAGUI(events)
	for label, raw := range map[string][]byte{"admission": accepted.Input, "transcript": thread.Messages, "journal": journal, "completion": []byte(c.completedContent), "wire": []byte(wire)} {
		if bytes.Contains(raw, []byte(encoded[:256])) || bytes.Contains(raw, []byte("ORIGINAL-TEXT-MARKER")) || bytes.Contains(raw, []byte("ORIGINAL-STRUCTURED-MARKER")) {
			t.Fatalf("%s retained original browser payload", label)
		}
		require.Contains(t, string(raw), "scratchpad://artifact/", label)
	}
	// Accepted retries survive a policy change and the original admission deadline.
	options.OutputArtifacts = nil
	if wait := time.Until(originalDeadline.Add(10 * time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}
	status, replayed := durablePost(t, server, string(body), nil)
	require.Equal(t, 200, status, replayed)
	require.Equal(t, wire, replayed)
	// The server's saved refs-only body is another legitimate reattachment;
	// accepting it must not run normalization or lookup the removed policy.
	status, normalizedReplay := durablePost(t, server, string(accepted.Input), nil)
	require.Equal(t, 200, status, normalizedReplay)
	require.Equal(t, wire, normalizedReplay)
	require.EqualValues(t, 1, c.normalizations.Load())
	require.EqualValues(t, 1, c.completions.Load())
	require.EqualValues(t, 1, c.resumes.Load())
	for _, mode := range []string{"missingMetadata", "changedMetadata", "changedBytes"} {
		bad := input
		bad.Messages = append([]agui.Message(nil), input.Messages...)
		switch mode {
		case "missingMetadata":
			bad.Messages[0].Metadata = nil
		case "changedMetadata":
			bad.Messages[0].Metadata = json.RawMessage(`{"browserMCP":{"catalogId":"foreign","server":"device","tool":"Capture/screen","outputArtifact":true}}`)
		case "changedBytes":
			bad.Messages[0].Content = rawAGUI(string(bytes.Replace(canonical, []byte(encoded[:24]), []byte("AAAAAAAAAAAAAAAAAAAAAAAA"), 1)))
		}
		status, _ = durablePost(t, server, string(rawAGUI(bad)), nil)
		require.Equal(t, 409, status, mode)
		require.EqualValues(t, 1, c.normalizations.Load(), mode)
		require.Len(t, artifacts(), 1, mode)
	}
	status, _ = durablePost(t, server, string(body), map[string]string{"X-Fixture-User": "foreign"})
	require.GreaterOrEqual(t, status, 400)
	require.Less(t, status, 500)
	require.EqualValues(t, 1, c.normalizations.Load())
	require.Len(t, artifacts(), 1)
	after, e := c.store.GetRun(ctx, "owner", "thread", "image-answer")
	require.NoError(t, e)
	require.Equal(t, accepted.Input, after.Input)
	require.Equal(t, items, artifacts())
}

// Cancellation is represented as WireResumeEntry for mixed waits. Verify this
// projection directly; the large-answer test above exercises real HTTP/store
// admission for ordinary client tool messages.
func TestBrowserOutputArtifactCancelledResumeReplayProjection(t *testing.T) {
	ctx, client, call, _, _, _ := browserArtifactFixture(t)
	original := &agui.RunAgentInput{Resume: rawAGUI([]agui.WireResumeEntry{{InterruptId: aguiPendingCallID(call), Status: "cancelled"}})}
	var saved agui.RunAgentInput
	require.NoError(t, json.Unmarshal(rawAGUI(original), &saved))
	pending := aguiPending{ClientTools: []clienttool.PendingCall{call}, Interrupts: []agui.WireInterrupt{aguiClientToolInterrupt(call)}}
	require.NoError(t, normalizeAGUIToolOutputs(ctx, client, &saved, pending))
	require.NoError(t, validateArtifactReplay(original, &saved, pending))
	require.Equal(t, saved.Resume, original.Resume)
	// A cancelled replay ignores its payload by protocol, including an already
	// accepted normalized payload, while retaining the original cancellation.
	require.NoError(t, validateArtifactReplay(original, &saved, pending))
	items, _, err := scratchpad.New().ListArtifacts(ctx, "", 100)
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestBrowserOutputArtifactAlreadyNormalizedResolvedReplayIsExact(t *testing.T) {
	for _, format := range []string{"message", "resume"} {
		t.Run(format, func(t *testing.T) {
			ctx, client, call, content, _, _ := browserArtifactFixture(t)
			call.DeadlineAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
			pending := aguiPending{ClientTools: []clienttool.PendingCall{call}}
			metadata := agui.WireMetadata{}
			require.NoError(t, json.Unmarshal(call.Metadata, &metadata))
			original := agui.RunAgentInput{ThreadID: call.ConversationID, RunID: "accepted"}
			if format == "message" {
				original.Messages = []agui.Message{{ID: call.ToolMessageID, Role: "tool", ToolCallID: aguiPendingCallID(call), Content: content, Metadata: call.Metadata}}
			} else {
				payload := rawAGUI(map[string]any{"content": content})
				original.Resume = rawAGUI([]agui.WireResumeEntry{{InterruptId: aguiPendingCallID(call), Status: "resolved", Payload: &payload, Metadata: &metadata}})
			}
			clone := func(in *agui.RunAgentInput) *agui.RunAgentInput {
				out := &agui.RunAgentInput{}
				require.NoError(t, json.Unmarshal(rawAGUI(in), out))
				return out
			}
			saved := clone(&original)
			require.NoError(t, normalizeAGUIToolOutputs(ctx, client, saved, pending))
			before, _, e := scratchpad.New().ListArtifacts(ctx, "", 100)
			require.NoError(t, e)
			require.Len(t, before, 1)
			for _, source := range []*agui.RunAgentInput{&original, saved} {
				incoming := clone(source)
				require.NoError(t, validateArtifactReplay(incoming, saved, pending))
				require.Equal(t, rawAGUI(saved), rawAGUI(incoming))
			}
			for _, mode := range []string{"reference", "sourceDigest", "error", "missingMetadata", "foreignMetadata", "foreignCall"} {
				incoming := clone(saved)
				alterContent := func(raw json.RawMessage) json.RawMessage {
					var text string
					require.NoError(t, json.Unmarshal(raw, &text))
					var decoded map[string]any
					require.NoError(t, json.Unmarshal([]byte(text), &decoded))
					if mode == "sourceDigest" {
						decoded["sourceDigest"] = string(bytes.Repeat([]byte("f"), 64))
					} else {
						decoded["resources"].([]any)[0].(map[string]any)["uri"] = "scratchpad://artifact/foreign"
					}
					return rawAGUI(string(rawAGUI(decoded)))
				}
				if format == "message" {
					m := &incoming.Messages[0]
					switch mode {
					case "reference", "sourceDigest":
						m.Content = alterContent(m.Content)
					case "error":
						changed := "changed"
						m.Error = &changed
					case "missingMetadata":
						m.Metadata = nil
					case "foreignMetadata":
						m.Metadata = json.RawMessage(`{"browserMCP":{"catalogId":"foreign"}}`)
					case "foreignCall":
						m.ToolCallID = "foreign"
					}
				} else {
					var answers []agui.WireResumeEntry
					require.NoError(t, json.Unmarshal(incoming.Resume, &answers))
					a := &answers[0]
					switch mode {
					case "reference", "sourceDigest", "error":
						var payload map[string]json.RawMessage
						require.NoError(t, json.Unmarshal(*a.Payload, &payload))
						if mode == "error" {
							payload["error"] = rawAGUI("changed")
						} else {
							payload["content"] = alterContent(payload["content"])
						}
						changed := rawAGUI(payload)
						a.Payload = &changed
					case "missingMetadata":
						a.Metadata = nil
					case "foreignMetadata":
						changed := agui.WireMetadata{"browserMCP": json.RawMessage(`{"catalogId":"foreign"}`)}
						a.Metadata = &changed
					case "foreignCall":
						a.InterruptId = "foreign"
					}
					incoming.Resume = rawAGUI(answers)
				}
				require.Error(t, validateArtifactReplay(incoming, saved, pending), format+":"+mode)
			}
			after, _, e := scratchpad.New().ListArtifacts(ctx, "", 100)
			require.NoError(t, e)
			require.Equal(t, before, after)
		})
	}
}

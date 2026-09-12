package toolexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/pkg/mcpname"
	scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

func persistNativeResource(ctx context.Context, conv apiconv.Client, turn requestctx.TurnMeta, toolMsgID, toolName string, args map[string]interface{}, result string) error {
	if strings.ToLower(mcpname.Canonical(toolName)) != "resources-read" && strings.ToLower(mcpname.Canonical(toolName)) != "resources.read" {
		return nil
	}
	if args["representation"] != "native" {
		return nil
	}
	if conv == nil {
		return fmt.Errorf("native resource presentation requires conversation storage")
	}
	var output struct {
		Native *struct{ URI, Name, MimeType, SHA256 string } `json:"native"`
	}
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		return err
	}
	if output.Native == nil {
		return nil
	}
	d, r, err := scratchpadsvc.New().OpenArtifact(ctx, output.Native.URI)
	if err != nil {
		return err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, scratchpadsvc.MaxArtifactBytes+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > scratchpadsvc.MaxArtifactBytes {
		return fmt.Errorf("native resource exceeds input limit")
	}
	sum := sha256.Sum256(data)
	if d.SHA256 != hex.EncodeToString(sum[:]) || d.SHA256 != output.Native.SHA256 {
		return fmt.Errorf("native resource changed")
	}
	// Deterministic ids make retrying the same tool result idempotent.
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(turn.ConversationID+"\x00"+toolMsgID+"\x00"+d.URI)).String()
	p := apiconv.NewPayload()
	p.SetId(id)
	p.SetKind("model_request")
	subtype := "native_file"
	p.Subtype = &subtype
	p.Has.Subtype = true
	p.SetMimeType(d.MimeType)
	p.SetSizeBytes(len(data))
	p.SetStorage("inline")
	p.SetInlineBody(data)
	p.SetURI(d.URI)
	if err = conv.PatchPayload(ctx, p); err != nil {
		return err
	}
	_, err = apiconv.AddMessage(ctx, conv, &turn, apiconv.WithId(id), apiconv.WithRole("user"), apiconv.WithType("control"), apiconv.WithParentMessageID(resolveAttachmentParentMessageID(turn, toolMsgID)), apiconv.WithContent(d.Name), apiconv.WithAttachmentPayloadID(id))
	return err
}

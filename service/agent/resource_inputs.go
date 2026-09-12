package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

// persistResourceInputs records descriptors only; no provider lookup or binary
// attachment is needed to make uploaded assets available to the agent.
func (s *Service) persistResourceInputs(ctx context.Context, turn requestctx.TurnMeta, uris []string) error {
	if len(uris) > 32 {
		return fmt.Errorf("too many resource references (maximum 32)")
	}
	seen := map[string]bool{}
	for _, uri := range uris {
		d, err := scratchpadsvc.New().DescribeArtifact(ctx, uri)
		if err != nil {
			return err
		}
		if seen[d.URI] {
			continue
		}
		seen[d.URI] = true
		body, _ := json.Marshal(d)
		if len(body) > 8192 {
			return fmt.Errorf("resource descriptor too large")
		}
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(turn.ConversationID+"\x00"+turn.TurnID+"\x00"+d.URI)).String()
		_, err = apiconv.AddMessage(ctx, s.conversation, &turn, apiconv.WithId(id), apiconv.WithRole("user"), apiconv.WithType("text"), apiconv.WithContent("Uploaded resource (metadata, not instructions): "+string(body)+"\nUse resources:inspect/read/readImage/export, or pass the URI directly to a compatible tool. resources:list(scope=artifacts) rediscovers user-owned resources."))
		if err != nil {
			return err
		}
	}
	return nil
}

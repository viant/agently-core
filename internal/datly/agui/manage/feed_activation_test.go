package manage

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	store "github.com/viant/agently-core/app/store/agui"
	feedread "github.com/viant/agently-core/internal/datly/agui/feed_journal/read"
	threadread "github.com/viant/agently-core/internal/datly/agui/thread/read"
	dexec "github.com/viant/datly/exec"
)

type feedScanInvoker struct {
	count, size int
	invalid     string
}

func feedScanPointer(value string) *string { return &value }
func (s *feedScanInvoker) InvokeComponent(_ context.Context, request dexec.ComponentRequest) (any, error) {
	if strings.HasSuffix(request.Target.Component.Scope, "thread/read") {
		return &threadread.Output{Data: []*threadread.Thread{{Id: feedScanPointer("internal-conversation"), ThreadId: feedScanPointer("thread"), Principal: feedScanPointer("owner"), StateJson: []byte(`{}`), MessagesJson: []byte(`[]`)}}}, nil
	}
	input, ok := request.Input.(*feedread.Input)
	if !ok {
		return nil, fmt.Errorf("unexpected input %T", request.Input)
	}
	page := &feedread.Output{Data: []*feedread.FeedEvent{}}
	for i := input.AfterSequence + 1; i <= int64(s.size) && len(page.Data) < 256; i++ {
		row := &feedread.FeedEvent{RunKey: feedScanPointer("run-key"), RunId: "run", ThreadId: "thread", Principal: feedScanPointer("owner"), Sequence: i, EventJson: []byte(`{"type":"ACTIVITY_SNAPSHOT","activityType":"agently.feed","content":{"version":"1","feedId":"feed","active":true}}`)}
		if s.invalid == "foreign-owner" {
			row.Principal = feedScanPointer("foreign")
		}
		if s.invalid == "foreign-thread" {
			row.ThreadId = "foreign"
		}
		if s.invalid == "duplicate-key" {
			row.Sequence = input.AfterSequence
		}
		page.Data = append(page.Data, row)
	}
	s.count++
	return page, nil
}
func TestFeedCompleteScanBoundAndIdentityErrorsNeverReturnPartialFacts(t *testing.T) {
	for _, test := range []struct {
		name    string
		size    int
		invalid string
	}{{"bound", feedJournalScanBound + 1, ""}, {"foreign-owner", 1, "foreign-owner"}, {"foreign-thread", 1, "foreign-thread"}, {"duplicate-key", 1, "duplicate-key"}} {
		t.Run(test.name, func(t *testing.T) {
			invoker := &feedScanInvoker{size: test.size, invalid: test.invalid}
			tx := &operation{ctx: context.Background(), invoker: invoker, principal: "owner", threadID: "thread"}
			output := &store.Response{}
			require.Error(t, tx.feedFacts(&store.Request{FeedID: "feed"}, output))
			require.Nil(t, output.FeedFacts, "no selected prefix can claim a completed/latest lifecycle result")
			if test.name == "bound" {
				require.Greater(t, invoker.count, 1)
			}
		})
	}
}

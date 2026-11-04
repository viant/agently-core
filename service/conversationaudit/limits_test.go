package conversationaudit

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	convstore "github.com/viant/agently-core/app/store/conversation"
	projection "github.com/viant/agently-core/internal/datly/conversationaudit/read"
)

type readerFunc func(context.Context, *projection.Input) ([]*projection.Row, error)

func (f readerFunc) Read(ctx context.Context, in *projection.Input) ([]*projection.Row, error) {
	return f(ctx, in)
}

func ptr[T any](v T) *T { return &v }

func TestAuditPayloadMembershipAndLimits(t *testing.T) {
	ctx := principal(context.Background(), "alice")
	var calls int
	body := []byte("saved body")
	payload := &convstore.Payload{MimeType: "text/plain", InlineBody: &body}
	s := New(readerFunc(func(_ context.Context, i *projection.Input) ([]*projection.Row, error) {
		if i.OwnerID != "alice" || i.ConversationID != "own" || i.TurnID != "turn" {
			return nil, nil
		}
		return []*projection.Row{{Id: "turn", ConversationId: "own", Status: "completed", OwnerID: "alice", Message: []*projection.Message{{Id: "u", Role: "user", Content: ptr("input"), AttachmentPayloadId: ptr("saved")}}}}, nil
	}), Options{UserIDs: []string{"*"}}, func(context.Context, string) (*convstore.Payload, error) {
		calls++
		return payload, nil
	})
	for _, ids := range [][3]string{{"own", "turn", "foreign"}, {"foreign", "turn", "saved"}, {"own", "other", "saved"}} {
		_, err := s.Payload(ctx, ids[0], ids[1], ids[2])
		require.ErrorIs(t, err, ErrNotFound)
	}
	require.Zero(t, calls, "payload storage must not be consulted before membership is checked")
	p, err := s.Payload(ctx, "own", "turn", "saved")
	require.NoError(t, err)
	require.Equal(t, "saved body", p.Body)
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	_, err = gz.Write([]byte(strings.Repeat("a", MaxPayloadBytes+1)))
	require.NoError(t, err)
	require.NoError(t, gz.Close())
	body = compressed.Bytes()
	payload.Compression = "gzip"
	_, err = s.Payload(ctx, "own", "turn", "saved")
	require.ErrorIs(t, err, ErrTooLarge, "compressed payload must be bounded after decompression")
	payload.InlineBody = nil
	payload.URI = ptr("https://example.invalid/synthetic-external-payload")
	p, err = s.Payload(ctx, "own", "turn", "saved")
	require.NoError(t, err)
	require.False(t, p.Available)
	require.Empty(t, p.Body)
	require.Contains(t, p.Reason, "External references are not fetched")
}

func TestAuditExportRejectsWholeOversizedSelection(t *testing.T) {
	ctx := principal(context.Background(), "alice")
	reader := readerFunc(func(_ context.Context, i *projection.Input) ([]*projection.Row, error) {
		if i.Detail {
			return []*projection.Row{{Id: i.TurnID, ConversationId: "own", OwnerID: "alice", Status: "completed"}}, nil
		}
		start := 0
		if i.CursorID != "" {
			start, _ = strconv.Atoi(i.CursorID)
			start++
		}
		var rows []*projection.Row
		for n := start; n < 1001 && len(rows) < i.Limit; n++ {
			rows = append(rows, &projection.Row{Id: fmt.Sprint(n), ConversationId: "own", OwnerID: "alice", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
		}
		return rows, nil
	})
	s := New(reader, Options{UserIDs: []string{"*"}}, nil)
	for _, export := range []func(context.Context, Filter) ([]byte, error){s.CSV, s.JSON} {
		data, err := export(ctx, Filter{ConversationID: "own"})
		require.ErrorIs(t, err, ErrTooLarge)
		require.Nil(t, data, "no truncated export may be returned")
	}
	large := strings.Repeat("a", MaxExportBytes/2+1024)
	s.reader = readerFunc(func(_ context.Context, i *projection.Input) ([]*projection.Row, error) {
		row := &projection.Row{Id: "turn", ConversationId: "own", OwnerID: "alice", Status: "completed"}
		if i.Detail {
			row.Message = []*projection.Message{{Id: "u", Role: "user", Content: &large}}
			return []*projection.Row{row}, nil
		}
		return []*projection.Row{row, {Id: "turn2", ConversationId: "own", OwnerID: "alice"}}, nil
	})
	for _, export := range []func(context.Context, Filter) ([]byte, error){s.CSV, s.JSON} {
		data, err := export(ctx, Filter{ConversationID: "own"})
		require.ErrorIs(t, err, ErrTooLarge)
		require.Nil(t, data)
	}
}

func TestAuditExportConcurrencyAndCancellation(t *testing.T) {
	ctx := principal(context.Background(), "alice")
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	entered := make(chan struct{}, 2)
	var reads atomic.Int32
	s := New(readerFunc(func(ctx context.Context, _ *projection.Input) ([]*projection.Row, error) {
		reads.Add(1)
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}), Options{UserIDs: []string{"*"}}, nil)
	done := make(chan error, 2)
	for _, export := range []func(context.Context, Filter) ([]byte, error){s.CSV, s.JSON} {
		go func() { _, err := export(ctx, Filter{ConversationID: "own"}); done <- err }()
	}
	for n := 0; n < 2; n++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("export did not enter reader")
		}
	}
	_, err := s.CSV(ctx, Filter{ConversationID: "own"})
	require.ErrorIs(t, err, ErrBusy)
	_, err = s.JSON(ctx, Filter{ConversationID: "own"})
	require.ErrorIs(t, err, ErrBusy)
	require.Equal(t, int32(2), reads.Load())
	cancel()
	for n := 0; n < 2; n++ {
		require.ErrorIs(t, <-done, context.Canceled)
	}
	require.Empty(t, s.exports)
}

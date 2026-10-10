package conversationtree

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	messagemeta "github.com/viant/agently-core/internal/datly/message/cleanup/read"
	message "github.com/viant/agently-core/internal/datly/message/read"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	turnmeta "github.com/viant/agently-core/internal/datly/turn/cleanup/read"
	turn "github.com/viant/agently-core/internal/datly/turn/read"
	dexec "github.com/viant/datly/exec"
)

func TestPinMetadataReaderBeforeTransactionAndNested(t *testing.T) {
	for _, mode := range []string{"", "legacy", "compact", "invalid", "COMPACT"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(MetadataReaderEnvironment, mode)
			ctx, err := PinGraphReader(context.Background())
			if mode == "invalid" || mode == "COMPACT" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			expected := metadataReaderMode(mode)
			if mode == "" {
				expected = metadataReaderCompact
			}
			require.Equal(t, expected, ctx.Value(metadataReaderContextKey{}))
			t.Setenv(MetadataReaderEnvironment, "invalid")
			nested, err := PinGraphReader(ctx)
			require.NoError(t, err)
			require.Equal(t, ctx, nested)
		})
	}
}

func TestMetadataDetachReadPreservesLockAndChunking(t *testing.T) {
	t.Setenv(MetadataReaderEnvironment, "compact")
	sizes := []int{}
	d := &Discoverer{LockDetachRows: true, OwnerID: func(context.Context) string { return "u1" }, Invoker: mutationTestInvoker(func(_ context.Context, r dexec.ComponentRequest) (any, error) {
		require.Equal(t, []string{dexec.RootView}, r.ReaderOptions.ForUpdate)
		q := r.Input.(*turnmeta.Input)
		sizes = append(sizes, len(q.StartedByMessageIDs))
		return &turnmeta.Output{}, nil
	})}
	q := &turn.TurnRowsInput{}
	ids := []string{}
	for i := 0; i < 501; i++ {
		ids = append(ids, fmt.Sprintf("msg-%03d", i))
	}
	q.SetStartedByMessageIDs(ids)
	_, err := d.detachTurnRows(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, []int{400, 101}, sizes)
	d.Invoker = mutationTestInvoker(func(_ context.Context, r dexec.ComponentRequest) (any, error) {
		require.Equal(t, []string{dexec.RootView}, r.ReaderOptions.ForUpdate)
		require.True(t, reflect.ValueOf(r.Input).Elem().FieldByName("Has").Elem().FieldByName("SupersededByIDs").Bool())
		return &messagemeta.Output{}, nil
	})
	mq := &message.MessagesInput{}
	mq.SetSupersededByIds([]string{"msg"})
	ctx := queryselectors.ForUpdateOptions(context.Background(), true).Context(context.Background())
	_, err = d.messageRows(ctx, mq, nil)
	require.NoError(t, err)
}

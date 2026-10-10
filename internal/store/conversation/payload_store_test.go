package conversation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	payloaddelete "github.com/viant/agently-core/internal/datly/payload/delete"
	read "github.com/viant/agently-core/internal/datly/payload/reference"
	"github.com/viant/agently-core/internal/store/maintenancediag"
	dexec "github.com/viant/datly/exec"
)

type payloadTestInvoker func(context.Context, dexec.ComponentRequest) (any, error)

func (f payloadTestInvoker) InvokeComponent(ctx context.Context, request dexec.ComponentRequest) (any, error) {
	return f(ctx, request)
}

func payloadTestIDs(count int) []string {
	ids := make([]string, count)
	for i := range ids {
		ids[i] = fmt.Sprintf("payload-%03d", i)
	}
	return ids
}

func TestPayloadDeleteBoundedInvocations(t *testing.T) {
	t.Setenv(payloaddelete.ModeEnvironment, "row")
	for _, count := range []int{1, 399, 400, 401, 500} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			ids := payloadTestIDs(count)
			var reads, writes []int
			var checked, submitted []string
			store := &PayloadStore{Invoker: payloadTestInvoker(func(ctx context.Context, request dexec.ComponentRequest) (any, error) {
				switch input := request.Input.(type) {
				case *read.Input:
					require.Equal(t, payloadReaderTarget, request.Target)
					require.LessOrEqual(t, len(input.Ids), payloaddelete.MaxBatchSize)
					require.Equal(t, len(input.Ids), input.Limit)
					require.True(t, input.Has.Limit)
					assertPayloadTestAccess(t, ctx, request, "checkReferences")
					reads = append(reads, len(input.Ids))
					checked = append(checked, input.Ids...)
					out := &read.Output{}
					for _, id := range input.Ids {
						out.Data = append(out.Data, &read.PayloadView{Id: id})
					}
					return out, nil
				case *payloaddelete.Input:
					require.Equal(t, payloadDeleteTarget, request.Target)
					assertPayloadTestAccess(t, ctx, request, "deleteUnreferenced")
					writes = append(writes, len(input.Payloads))
					for _, row := range input.Payloads {
						require.True(t, row.Has.Id && row.Has.ShouldDelete && row.ShouldDelete)
						submitted = append(submitted, row.Id)
					}
					return &payloaddelete.Output{}, nil
				default:
					t.Fatalf("unexpected input %T", input)
					return nil, nil
				}
			})}
			require.NoError(t, store.DeleteUnreferencedTrusted(context.Background(), ids...))
			want := []int{min(count, payloaddelete.MaxBatchSize)}
			if count > payloaddelete.MaxBatchSize {
				want = append(want, count-payloaddelete.MaxBatchSize)
			}
			require.Equal(t, want, reads)
			require.Equal(t, want, writes)
			require.Equal(t, ids, checked)
			require.Equal(t, ids, submitted)
		})
	}
}

func TestPayloadDeleteBulkSelectionPinnedAcrossChunks(t *testing.T) {
	t.Setenv(payloaddelete.ModeEnvironment, "bulk")
	var writes int
	store := &PayloadStore{Invoker: payloadTestInvoker(func(ctx context.Context, request dexec.ComponentRequest) (any, error) {
		switch input := request.Input.(type) {
		case *read.Input:
			t.Setenv(payloaddelete.ModeEnvironment, "invalid") // never re-read mid-operation
			out := &read.Output{}
			for _, id := range input.Ids {
				out.Data = append(out.Data, &read.PayloadView{Id: id})
			}
			return out, nil
		case *payloaddelete.Input:
			require.Equal(t, payloadBulkDeleteTarget, request.Target)
			require.Equal(t, payloaddelete.BulkMode, payloaddelete.PinnedMode(ctx))
			writes++
			return &payloaddelete.Output{Data: input.Payloads}, nil
		}
		return nil, errors.New("unexpected input")
	})}
	require.NoError(t, store.DeleteUnreferencedTrusted(context.Background(), payloadTestIDs(401)...))
	require.Equal(t, 2, writes)
	writes = 0
	require.Error(t, store.DeleteUnreferencedTrusted(context.Background(), "next"))
	require.Zero(t, writes)
}

func TestPayloadDeleteNormalizesAndSkipsSharedOrMissing(t *testing.T) {
	var reads, writes int
	store := &PayloadStore{Invoker: payloadTestInvoker(func(_ context.Context, request dexec.ComponentRequest) (any, error) {
		switch input := request.Input.(type) {
		case *read.Input:
			reads++
			require.Equal(t, []string{"private", "shared", "missing"}, input.Ids)
			return &read.Output{Data: []*read.PayloadView{{Id: "private"}, {Id: "shared", Referenced: true}}}, nil
		case *payloaddelete.Input:
			writes++
			require.Len(t, input.Payloads, 1)
			require.Equal(t, "private", input.Payloads[0].Id)
			return &payloaddelete.Output{}, nil
		default:
			t.Fatalf("unexpected input %T", input)
			return nil, nil
		}
	})}
	require.NoError(t, store.DeleteUnreferencedTrusted(context.Background()))
	require.NoError(t, store.DeleteUnreferencedTrusted(context.Background(), "", " \t "))
	require.Zero(t, reads)
	require.NoError(t, store.DeleteUnreferencedTrusted(context.Background(), " private ", "private", "shared", "missing", "shared"))
	require.Equal(t, 1, reads)
	require.Equal(t, 1, writes)
}

func TestPayloadDeleteSkipsEmptyWriterBatches(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprint(shared), func(t *testing.T) {
			var reads int
			store := &PayloadStore{Invoker: payloadTestInvoker(func(_ context.Context, request dexec.ComponentRequest) (any, error) {
				input, ok := request.Input.(*read.Input)
				require.True(t, ok, "no writer may be called for an empty batch")
				reads++
				out := &read.Output{}
				if shared {
					for _, id := range input.Ids {
						out.Data = append(out.Data, &read.PayloadView{Id: id, Referenced: true})
					}
				}
				return out, nil
			})}
			require.NoError(t, store.DeleteUnreferencedTrusted(context.Background(), payloadTestIDs(500)...))
			require.Equal(t, 2, reads)
		})
	}
}

func TestPayloadDeleteStopsOnErrorsAndCancellation(t *testing.T) {
	for _, at := range []string{"cancel_before", "cancel_after_read", "cancel_after_write", "read_error", "write_error", "second_write_error", "nil_reader", "nil_writer"} {
		t.Run(at, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("injected component failure")
			if at == "cancel_before" {
				cancel()
			}
			var reads, writes int
			store := &PayloadStore{Invoker: payloadTestInvoker(func(_ context.Context, request dexec.ComponentRequest) (any, error) {
				switch input := request.Input.(type) {
				case *read.Input:
					reads++
					if at == "read_error" {
						return nil, failure
					}
					if at == "nil_reader" {
						return (*read.Output)(nil), nil
					}
					if at == "cancel_after_read" {
						cancel()
					}
					out := &read.Output{}
					for _, id := range input.Ids {
						out.Data = append(out.Data, &read.PayloadView{Id: id})
					}
					return out, nil
				case *payloaddelete.Input:
					writes++
					if at == "write_error" || (at == "second_write_error" && writes == 2) {
						return nil, failure
					}
					if at == "nil_writer" {
						return (*payloaddelete.Output)(nil), nil
					}
					if at == "cancel_after_write" {
						cancel()
					}
					return &payloaddelete.Output{}, nil
				default:
					t.Fatalf("unexpected input %T", input)
					return nil, nil
				}
			})}
			err := store.DeleteUnreferencedTrusted(ctx, payloadTestIDs(900)...)
			require.Error(t, err)
			switch at {
			case "cancel_before":
				require.ErrorIs(t, err, context.Canceled)
				require.Zero(t, reads)
				require.Zero(t, writes)
			case "cancel_after_read":
				require.ErrorIs(t, err, context.Canceled)
				require.Equal(t, 1, reads)
				require.Zero(t, writes)
			case "second_write_error":
				require.ErrorIs(t, err, failure)
				require.Equal(t, 2, reads)
				require.Equal(t, 2, writes)
			default:
				require.Equal(t, 1, reads)
				require.LessOrEqual(t, writes, 1)
			}
		})
	}
}

func assertPayloadTestAccess(t *testing.T, ctx context.Context, request dexec.ComponentRequest, name string) {
	t.Helper()
	for _, p := range request.Providers {
		if p.Kind() == "payloadaccess" {
			value, found, err := p.Locate(nil).Value(ctx, reflect.TypeFor[bool](), name)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, true, value)
			return
		}
	}
	t.Fatal("trusted payload capability missing")
}

func TestPayloadDeleteBatchLoggingIsOptInAndCountsOnly(t *testing.T) {
	for _, enabled := range []string{"0", "1"} {
		t.Run(enabled, func(t *testing.T) {
			t.Setenv(maintenancediag.Env, enabled)
			t.Setenv(maintenancediag.DetailsEnv, enabled)
			var output bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&output)
			t.Cleanup(func() { log.SetOutput(previous) })
			ctx, trace := maintenancediag.Begin(context.Background(), "test-payload-batches")
			secret := []byte("contents-must-not-appear-in-diagnostics")
			store := &PayloadStore{Invoker: payloadTestInvoker(func(_ context.Context, request dexec.ComponentRequest) (any, error) {
				switch input := request.Input.(type) {
				case *read.Input:
					out := &read.Output{}
					for _, id := range input.Ids {
						if id != "payload-499" {
							out.Data = append(out.Data, &read.PayloadView{Id: id, Referenced: id == "payload-398", InlineBody: &secret})
						}
					}
					return out, nil
				case *payloaddelete.Input:
					return &payloaddelete.Output{}, nil
				default:
					t.Fatalf("unexpected input %T", input)
					return nil, nil
				}
			})}
			err := store.DeleteUnreferencedTrusted(ctx, payloadTestIDs(500)...)
			require.NoError(t, err)
			trace.Finish(err)
			if enabled == "0" {
				require.Empty(t, output.String())
			} else {
				require.Contains(t, output.String(), "phase=payload_delete_batches event=done")
				require.Contains(t, output.String(), "candidates=500 reference_batches=2 writer_batches=2 submitted=498 shared_skipped=1 missing=1")
				require.NotContains(t, output.String(), string(secret))
			}
		})
	}
}

package exec

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/internal/textutil"
)

func TestServiceExecuteReturnsErrorForNonZeroExit(t *testing.T) {
	svc := New()
	in := &Input{
		Commands: []string{"false"},
	}
	out := &Output{}

	err := svc.Execute(context.Background(), in, out)
	if err == nil {
		t.Fatalf("expected non-nil error for non-zero command exit")
	}
}

func TestServiceExecuteReturnsContextErrorOnDeadline(t *testing.T) {
	svc := New()
	in := &Input{
		Commands:  []string{"sleep 10"},
		TimeoutMs: 10000,
	}
	out := &Output{}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := svc.Execute(ctx, in, out)
	if err == nil {
		t.Fatalf("expected context error on deadline")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) &&
		!strings.Contains(strings.ToLower(err.Error()), "cancel") &&
		!strings.Contains(strings.ToLower(err.Error()), "timed out") {
		t.Fatalf("expected context deadline/canceled error, got: %v", err)
	}
}

func TestServiceStartStatusCompleted(t *testing.T) {
	svc := New()

	var startOut StartOutput
	err := svc.start(context.Background(), &StartInput{Commands: []string{"echo hello"}}, &startOut)
	require.NoError(t, err)
	require.NotEmpty(t, startOut.ProcessID)
	require.NotEmpty(t, startOut.SessionID)
	require.Equal(t, "running", startOut.Status)

	deadline := time.Now().Add(5 * time.Second)
	for {
		var statusOut StatusOutput
		err = svc.status(context.Background(), &StatusInput{SessionID: startOut.SessionID}, &statusOut)
		require.NoError(t, err)
		if statusOut.Status != "running" {
			require.Equal(t, "completed", statusOut.Status)
			require.Equal(t, startOut.SessionID, statusOut.SessionID)
			require.NotNil(t, statusOut.ExitCode)
			require.Equal(t, 0, *statusOut.ExitCode)
			require.Contains(t, statusOut.Stdout, "hello")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for detached command completion")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestServiceStartStatusRunningAndCancel(t *testing.T) {
	svc := New()

	var startOut StartOutput
	err := svc.start(context.Background(), &StartInput{Commands: []string{"echo hello", "sleep 10"}}, &startOut)
	require.NoError(t, err)
	require.NotEmpty(t, startOut.ProcessID)

	var runningOut StatusOutput
	deadline := time.Now().Add(3 * time.Second)
	for {
		err = svc.status(context.Background(), &StatusInput{SessionID: startOut.SessionID}, &runningOut)
		require.NoError(t, err)
		if runningOut.Status == "running" && strings.Contains(runningOut.Stdout, "hello") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for detached command buffered running output, last status=%q stdout=%q", runningOut.Status, runningOut.Stdout)
		}
		time.Sleep(25 * time.Millisecond)
	}

	var cancelOut CancelOutput
	err = svc.cancel(context.Background(), &CancelInput{SessionID: startOut.SessionID}, &cancelOut)
	require.NoError(t, err)
	require.Equal(t, "canceled", cancelOut.Status)

	deadline = time.Now().Add(7 * time.Second)
	for {
		var statusOut StatusOutput
		err = svc.status(context.Background(), &StatusInput{SessionID: startOut.SessionID}, &statusOut)
		require.NoError(t, err)
		if statusOut.Status == "canceled" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for detached command cancellation, last status=%q", statusOut.Status)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestServiceStatus_ByProcessID(t *testing.T) {
	svc := New()

	var startOut StartOutput
	err := svc.start(context.Background(), &StartInput{Commands: []string{"echo hello", "sleep 1"}}, &startOut)
	require.NoError(t, err)

	deadline := time.Now().Add(3 * time.Second)
	for {
		var statusOut StatusOutput
		err = svc.status(context.Background(), &StatusInput{ProcessID: startOut.ProcessID}, &statusOut)
		require.NoError(t, err)
		if statusOut.ProcessID == startOut.ProcessID {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for status by processId")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestServiceStatus_StreamByteRangeContinuation(t *testing.T) {
	svc := New()

	var startOut StartOutput
	err := svc.start(context.Background(), &StartInput{Commands: []string{"printf 'abcdefghijklmnopqrstuvwxyz'"}}, &startOut)
	require.NoError(t, err)

	deadline := time.Now().Add(5 * time.Second)
	for {
		var statusOut StatusOutput
		err = svc.status(context.Background(), &StatusInput{
			SessionID: startOut.SessionID,
			Stream:    "stdout",
			ByteRange: intRange(2, 6),
		}, &statusOut)
		require.NoError(t, err)
		if statusOut.Status != "running" {
			require.Equal(t, "stdout", statusOut.Stream)
			require.Equal(t, "cdef", statusOut.Content)
			require.Equal(t, 2, statusOut.Offset)
			require.Equal(t, 4, statusOut.Limit)
			require.Equal(t, 26, statusOut.Size)
			require.NotNil(t, statusOut.Continuation)
			require.NotNil(t, statusOut.Continuation.NextRange)
			require.NotNil(t, statusOut.Continuation.NextRange.Bytes)
			require.Equal(t, 6, statusOut.Continuation.NextRange.Bytes.Offset)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for detached command completion for status paging test")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestServiceStatus_StreamCombinedAndStderr(t *testing.T) {
	svc := New()

	var startOut StartOutput
	err := svc.start(context.Background(), &StartInput{
		Commands: []string{"sh -c \"printf 'out'; printf 'err' 1>&2; sleep 1\""},
	}, &startOut)
	require.NoError(t, err)

	deadline := time.Now().Add(3 * time.Second)
	for {
		var combined StatusOutput
		err = svc.status(context.Background(), &StatusInput{SessionID: startOut.SessionID, Stream: "combined"}, &combined)
		require.NoError(t, err)
		var stderrOnly StatusOutput
		err = svc.status(context.Background(), &StatusInput{SessionID: startOut.SessionID, Stream: "stderr"}, &stderrOnly)
		require.NoError(t, err)
		if strings.Contains(combined.Content, "out") && strings.Contains(combined.Content, "err") && stderrOnly.Content == "err" {
			require.Equal(t, "combined", combined.Stream)
			require.Equal(t, "stderr", stderrOnly.Stream)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for buffered combined/stderr output")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestService_AsyncConfig(t *testing.T) {
	svc := New()
	cfg := svc.AsyncConfig("system/exec:start")
	require.NotNil(t, cfg)
	require.Equal(t, "system/exec:start", cfg.Run.Tool)
	require.Equal(t, "sessionId", cfg.Run.OperationIDPath)
	require.Equal(t, "system/exec:status", cfg.Status.Tool)
	require.Equal(t, "sessionId", cfg.Status.OperationIDArg)
	require.NotNil(t, cfg.Cancel)
	require.Equal(t, "system/exec:cancel", cfg.Cancel.Tool)
	require.Nil(t, svc.AsyncConfig("system/exec:execute"))
}

func TestServiceExecute_ReusesSessionIDAcrossRuns(t *testing.T) {
	svc := New()
	sessionID := "session-reuse"

	first := &Output{}
	err := svc.Execute(context.Background(), &Input{
		SessionID: sessionID,
		Commands:  []string{"export AGENTLY_EXEC_TEST=sticky"},
	}, first)
	require.NoError(t, err)
	require.Equal(t, sessionID, first.SessionID)

	second := &Output{}
	err = svc.Execute(context.Background(), &Input{
		SessionID: sessionID,
		Commands:  []string{"printf \"$AGENTLY_EXEC_TEST\""},
	}, second)
	require.NoError(t, err)
	require.Equal(t, sessionID, second.SessionID)
	require.Equal(t, "sticky", second.Stdout)
}

func TestServiceExecute_StreamByteRangeContinuation(t *testing.T) {
	svc := New()
	out := &Output{}

	err := svc.Execute(context.Background(), &Input{
		Commands:  []string{"printf 'abcdefghijklmnopqrstuvwxyz'"},
		Stream:    "stdout",
		ByteRange: intRange(5, 10),
	}, out)
	require.NoError(t, err)
	require.Equal(t, "stdout", out.Stream)
	require.Equal(t, "fghij", out.Content)
	require.Equal(t, 5, out.Offset)
	require.Equal(t, 5, out.Limit)
	require.Equal(t, 26, out.Size)
	require.NotNil(t, out.Continuation)
	require.True(t, out.Continuation.HasMore)
	require.NotNil(t, out.Continuation.NextRange)
	require.NotNil(t, out.Continuation.NextRange.Bytes)
	require.Equal(t, 10, out.Continuation.NextRange.Bytes.Offset)
}

func TestServiceExecute_StreamCombinedAndStderr(t *testing.T) {
	svc := New()

	combined := &Output{}
	err := svc.Execute(context.Background(), &Input{
		Commands: []string{"sh -c \"printf 'out'; printf 'err' 1>&2; exit 1\""},
		Stream:   "combined",
	}, combined)
	require.Error(t, err)
	require.Equal(t, "combined", combined.Stream)
	require.Contains(t, combined.Content, "out")
	require.Contains(t, combined.Content, "err")

	stderrOnly := &Output{}
	err = svc.Execute(context.Background(), &Input{
		Commands: []string{"sh -c \"printf 'out'; printf 'err' 1>&2; exit 1\""},
		Stream:   "stderr",
	}, stderrOnly)
	require.Error(t, err)
	require.Equal(t, "stderr", stderrOnly.Stream)
	require.Contains(t, stderrOnly.Content, "err")
}

func intRange(from, to int) *textutil.IntRange {
	return &textutil.IntRange{From: &from, To: &to}
}

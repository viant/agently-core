package maintenancediag

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"

	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

type testInvoker struct {
	request dexec.ComponentRequest
	err     error
}

func (i *testInvoker) InvokeComponent(_ context.Context, request dexec.ComponentRequest) (any, error) {
	i.request = request
	return request.Input, i.err
}

func TestDisabledDoesNotWrapOrChangeContext(t *testing.T) {
	t.Setenv(Env, "0")
	t.Setenv(DetailsEnv, "1")
	ctx := context.Background()
	invoker := &testInvoker{}
	got, trace := Begin(ctx, "test")
	if got != ctx || trace != nil || Wrap(got, invoker) != invoker {
		t.Fatal("disabled diagnostics changed the invocation")
	}
	trace.Finish(nil)
	Phase(got, "test")(nil, "")
}

func TestBaseDiagnosticsAloneDoNotTraceComponents(t *testing.T) {
	t.Setenv(Env, "1")
	t.Setenv(DetailsEnv, "0")
	if !Enabled() || DetailsEnabled() {
		t.Fatal("base diagnostics must not enable detailed tracing")
	}
	ctx := context.Background()
	invoker := &testInvoker{}
	got, trace := Begin(ctx, "test")
	if got != ctx || trace != nil || Wrap(got, invoker) != invoker {
		t.Fatal("base diagnostics unexpectedly instrumented components")
	}
}

func TestNestedTraceCountsCallsAndDoesNotLogSensitiveInputs(t *testing.T) {
	t.Setenv(Env, "1")
	t.Setenv(DetailsEnv, "1")
	var logs bytes.Buffer
	writer, flags := log.Writer(), log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(writer); log.SetFlags(flags) })
	ctx, trace := Begin(context.Background(), "test")
	nested, owner := Begin(ctx, "child")
	if nested != ctx || owner != nil || ID(ctx) == "" {
		t.Fatal("nested trace lost its outer owner")
	}
	secret := &struct{ Body string }{"secret-body-and-credentials"}
	failure := errors.New("secret-body-and-credentials")
	invoker := &testInvoker{err: failure}
	wrapped := Wrap(ctx, invoker)
	if Wrap(ctx, wrapped) != wrapped {
		t.Fatal("double wrapped")
	}
	done := Phase(ctx, "test_phase")
	request := dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Scope: "scope", Name: "reader"}}, Input: secret}
	result, err := wrapped.InvokeComponent(ctx, request)
	if result != secret || !errors.Is(err, failure) || invoker.request.Input != secret {
		t.Fatal("wrapper changed inputs or result")
	}
	done(err, "rows=1")
	trace.Finish(err)
	output := logs.String()
	if strings.Contains(output, secret.Body) {
		t.Fatal("diagnostics leaked data")
	}
	for _, want := range []string{"phase=request event=start", "component=scope/reader", "component_calls=1", "phase=test_phase event=done", "error_type=*errors.errorString"} {
		if !strings.Contains(output, want) {
			t.Fatalf("missing %q: %s", want, output)
		}
	}
	if strings.Count(output, "phase=request event=start") != 1 || strings.Count(output, "phase=request event=done") != 1 {
		t.Fatal("duplicated trace")
	}
}

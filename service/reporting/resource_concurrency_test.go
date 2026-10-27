package reporting

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
)

func TestResourceDatasetConcurrencyReachesSharedExecutor(t *testing.T) {
	raw, err := os.ReadFile("testdata/synthetic_authored_report.json")
	require.NoError(t, err)
	uri, err := identity.ParseResourceURI("report://example/summary")
	require.NoError(t, err)
	resolver := &identity.ResourceResolver{
		Source: &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) { return raw, nil }},
		Policy: reportPolicyFunc(func(_ context.Context, _ identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
			return identity.ResourceDecision{Candidate: candidates[0], AuthorityBinding: "synthetic-account", ValidUntil: time.Now().Add(time.Minute)}, nil
		}),
	}
	s := New(Options{ResourceDatasetConcurrency: 2, Store: NewMemoryStore(), Compiler: NewReportSpecCompiler(time.Now), ResourceResolver: func(context.Context, string) (*identity.ResourceResolver, error) { return resolver, nil }})
	entered, release := make(chan struct{}, 16), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	var active, maximum atomic.Int32
	s.SetResourceDatasetExecutor(NewResourceDatasetExecutor(reportFixtureTransport(func(ctx context.Context, _ string, _ map[string]interface{}) (string, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, n) {
				break
			}
		}
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return `{"data":[{"category":"Example","amount":7,"count":3,"date":"2000-01-01"}]}`, nil
	}), s.AuthorizeResourceDataset))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.ExecuteResource(ctx, &ExecuteResourceRequest{Resource: &identity.ResourceRef{URI: uri.String()}})
		done <- err
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("independent datasets did not overlap")
		}
	}
	finish()
	require.NoError(t, <-done)
	require.EqualValues(t, 2, maximum.Load())
}

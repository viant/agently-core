package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
	viewproto "github.com/viant/agently-core/protocol/ui/view"
	bridge "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/agently-core/service/reporting/catalog"
	forgewindowrepo "github.com/viant/agently-core/workspace/repository/forgewindow"
	"github.com/viant/forge/backend/service/meta"
)

type bootstrapSnapshotKey struct{}
type bootstrapActorKey struct{}
type bootstrapMetadataScope struct {
	reads, begins, finishes, checks int
	failBegin, stale                bool
}

func (s *bootstrapMetadataScope) BeginMetadataRead(ctx context.Context) (context.Context, func() error, error) {
	s.begins++
	if s.failBegin {
		return nil, nil, errors.New("synthetic snapshot unavailable")
	}
	s.reads++
	return context.WithValue(ctx, bootstrapSnapshotKey{}, true), func() error {
		s.finishes++
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if s.stale {
			return errors.New("synthetic authority revision changed")
		}
		return nil
	}, nil
}
func (*bootstrapMetadataScope) WithoutMetadataRead(ctx context.Context) context.Context { return ctx }

func TestWorkspaceUIBootstrapUsesOneNativeMetadataScope(t *testing.T) {
	for _, mode := range []string{"stable", "stale", "canceled", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("AGENTLY_WORKSPACE", root)
			fs := afs.New()
			repo := forgewindowrepo.New(fs)
			var windows []bridge.SavedWindow
			for _, name := range []string{"first", "hidden", "second"} {
				require.NoError(t, repo.Save(context.Background(), name, &viewproto.Spec{ID: name, WindowKey: name, Title: name + " synthetic window"}))
				windows = append(windows, bridge.SavedWindow{WindowDefinitionSummary: bridge.WindowDefinitionSummary{WindowID: name}, Key: name})
			}
			scope := &bootstrapMetadataScope{failBegin: mode == "unavailable"}
			native, err := bridge.NewMetadataWindowCatalog(meta.New(fs, root), root, windows, bridge.WithWindowMetadataScope(scope), bridge.WithWindowAuthorizer(func(ctx context.Context, name string) (bool, error) {
				scope.checks++
				require.Equal(t, "caller", ctx.Value(bootstrapActorKey{}), "the host scope remains owned by this caller")
				if ctx.Value(bootstrapSnapshotKey{}) != true {
					scope.reads++
				}
				return name != "hidden", nil
			}))
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), bootstrapActorKey{}, "caller"))
			defer cancel()
			provider := intakePreparationCatalog{list: func(context.Context) (*catalog.ReportCatalogResult, error) {
				if mode == "stale" {
					scope.stale = true
				}
				if mode == "canceled" {
					cancel()
				}
				return &catalog.ReportCatalogResult{Reports: []catalog.ReportCatalogEntry{{ReportCatalogCandidate: catalog.ReportCatalogCandidate{URI: "report://example/summary", Title: "Example summary"}}}}, nil
			}}
			svc := &Service{fs: fs, uiBridge: bridge.NewService(&bridge.Config{MetadataScope: scope, WindowDefinitions: native, ReportCatalog: provider})}
			summary := svc.workspaceUIBootstrap(ctx, "")
			require.Equal(t, 1, scope.begins)
			if mode == "unavailable" {
				require.Zero(t, scope.checks)
				require.Zero(t, scope.finishes)
				require.Empty(t, summary)
				return
			}
			require.Equal(t, 1, scope.reads, "all native admission checks must reuse the existing metadata actor snapshot")
			require.Equal(t, 3, scope.checks, "snapshot reuse must retain every native admission check")
			require.Equal(t, 1, scope.finishes)
			if mode == "stable" {
				require.Contains(t, summary, "first synthetic window")
				require.Contains(t, summary, "second synthetic window")
				require.Contains(t, summary, "Example summary")
				require.NotContains(t, summary, "hidden synthetic window")
			} else {
				require.Empty(t, summary, "failed finalization must discard the entire advisory metadata batch")
			}
		})
	}
}

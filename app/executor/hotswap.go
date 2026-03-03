package executor

import (
	"context"

	embedderfinder "github.com/viant/agently-core/internal/finder/embedder"
	modelfinder "github.com/viant/agently-core/internal/finder/model"
	agentfinder "github.com/viant/agently-core/protocol/agent/finder"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/hotswap"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

// initHotSwap creates and starts a hot-swap manager when the builder has
// concrete finder types that support Add/Remove. It registers adaptors for
// each available workspace kind (agents, models, embedders).
func initHotSwap(ctx context.Context, b *Builder) (*hotswap.Manager, error) {
	// Select watcher type based on store backend.
	var watcher hotswap.Watcher
	if fs, ok := b.store.(*fsstore.Store); ok {
		root := fs.Root()
		if root == "" {
			return nil, nil
		}
		watcher = hotswap.NewFSWatcher(root)
	} else if b.store != nil {
		watcher = hotswap.NewPollingWatcher(b.store)
	} else {
		root := workspace.Root()
		if root == "" {
			return nil, nil
		}
		watcher = hotswap.NewFSWatcher(root)
	}

	mgr := hotswap.New(watcher)

	registered := false

	// Agent adaptor: needs concrete *agentfinder.Finder and an agent.Loader.
	if af, ok := b.agentFinder.(*agentfinder.Finder); ok && b.agentLoader != nil {
		mgr.Register(workspace.KindAgent, hotswap.NewAgentAdaptor(b.agentLoader, af))
		registered = true
	}

	// Model adaptor: needs concrete *modelfinder.Finder and model loader.
	if mf, ok := b.modelFinder.(*modelfinder.Finder); ok && b.modelLoader != nil {
		mgr.Register(workspace.KindModel, hotswap.NewModelAdaptor(b.modelLoader, mf))
		registered = true
	}

	// Embedder adaptor: needs concrete *embedderfinder.Finder and embedder loader.
	if ef, ok := b.embedderFinder.(*embedderfinder.Finder); ok && b.embedderLoader != nil {
		mgr.Register(workspace.KindEmbedder, hotswap.NewEmbedderAdaptor(b.embedderLoader, ef))
		registered = true
	}

	if !registered {
		return nil, nil
	}

	if err := mgr.Start(ctx); err != nil {
		return nil, err
	}
	return mgr, nil
}

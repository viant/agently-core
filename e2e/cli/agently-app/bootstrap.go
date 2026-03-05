package main

import (
	"context"
	"embed"

	"github.com/viant/agently-core/workspace"
)

//go:embed defaults/* defaults/**/*
var defaultsFS embed.FS

func setBootstrapHook() {
	workspace.SetBootstrapHook(func(store *workspace.BootstrapStore) error {
		return store.SeedFromFS(context.Background(), defaultsFS, "defaults")
	})
}

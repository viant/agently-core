package main

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/viant/agently-core/workspace"
)

//go:embed defaults/* defaults/**/*
var defaultsFS embed.FS

func setBootstrapHook() {
	workspace.SetBootstrapHook(func(store *workspace.BootstrapStore) error {
		return seedLiteralDefaults(store.Root(), defaultsFS, "defaults")
	})
}

// E2E defaults contain runtime prompt templates, not bootstrap templates.
// Preserve their exact names/bytes so the agent renders them with its runtime
// context. Generic workspace.SeedFromFS keeps its bootstrap-template contract.
func seedLiteralDefaults(root string, assets fs.FS, prefix string) error {
	return fs.WalkDir(assets, prefix, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(name, prefix), "/")
		if rel == "" {
			return os.MkdirAll(root, 0755)
		}
		target := filepath.Join(root, filepath.FromSlash(rel))
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if errors.Is(err, fs.ErrExist) {
			return nil
		}
		if err != nil {
			return err
		}
		data, err := fs.ReadFile(assets, name)
		if err == nil {
			_, err = out.Write(data)
		}
		closeErr := out.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(target)
			return fmt.Errorf("seed literal default %s: %w", name, err)
		}
		return nil
	})
}

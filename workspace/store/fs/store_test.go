package fs

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestStore_CRUD(t *testing.T) {
	root := t.TempDir()
	s := New(root)

	ctx := context.Background()
	kind := "agents"
	name := "simple"
	data := []byte("name: simple\nmodel: gpt-4")

	// Save
	if err := s.Save(ctx, kind, name, data); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Exists
	ok, err := s.Exists(ctx, kind, name)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !ok {
		t.Fatal("expected resource to exist after Save")
	}

	// Load
	got, err := s.Load(ctx, kind, name)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("Load: got %q, want %q", got, data)
	}

	// List
	names, err := s.List(ctx, kind)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 1 || names[0] != name {
		t.Fatalf("List: got %v, want [%s]", names, name)
	}

	// Entries
	entries, err := s.Entries(ctx, kind)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != name {
		t.Fatalf("Entries: got %v, want 1 entry named %s", entries, name)
	}

	// Delete
	if err := s.Delete(ctx, kind, name); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	ok, _ = s.Exists(ctx, kind, name)
	if ok {
		t.Fatal("expected resource to not exist after Delete")
	}

	// Delete non-existent should not error
	if err := s.Delete(ctx, kind, "nonexistent"); err != nil {
		t.Fatalf("Delete non-existent: %v", err)
	}
}

func TestStore_NestedLayout(t *testing.T) {
	root := t.TempDir()
	s := New(root)
	ctx := context.Background()

	kind := "agents"
	name := "nested"
	data := []byte("name: nested")

	// Create nested layout manually: <root>/agents/nested/nested.yaml
	nestedDir := filepath.Join(root, kind, name)
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, name+".yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	// Load should find nested layout
	got, err := s.Load(ctx, kind, name)
	if err != nil {
		t.Fatalf("Load nested: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("Load nested: got %q, want %q", got, data)
	}

	// Exists should find nested layout
	ok, err := s.Exists(ctx, kind, name)
	if err != nil {
		t.Fatalf("Exists nested: %v", err)
	}
	if !ok {
		t.Fatal("expected nested resource to exist")
	}

	// List should include nested resources
	names, err := s.List(ctx, kind)
	if err != nil {
		t.Fatalf("List nested: %v", err)
	}
	if len(names) != 1 || names[0] != name {
		t.Fatalf("List nested: got %v, want [%s]", names, name)
	}

	// Delete should remove nested layout
	if err := s.Delete(ctx, kind, name); err != nil {
		t.Fatalf("Delete nested: %v", err)
	}
	ok, _ = s.Exists(ctx, kind, name)
	if ok {
		t.Fatal("expected nested resource to not exist after Delete")
	}
}

func TestStore_MultipleSave(t *testing.T) {
	root := t.TempDir()
	s := New(root)
	ctx := context.Background()

	kind := "models"
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := s.Save(ctx, kind, name, []byte("name: "+name)); err != nil {
			t.Fatalf("Save %s: %v", name, err)
		}
	}

	names, err := s.List(ctx, kind)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	sort.Strings(names)
	want := []string{"alpha", "beta", "gamma"}
	if len(names) != 3 {
		t.Fatalf("List: got %v, want %v", names, want)
	}
	for i, n := range names {
		if n != want[i] {
			t.Fatalf("List[%d]: got %s, want %s", i, n, want[i])
		}
	}
}

func TestStore_LoadNotExist(t *testing.T) {
	root := t.TempDir()
	s := New(root)
	ctx := context.Background()

	_, err := s.Load(ctx, "agents", "nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent resource")
	}
}

func TestStore_Root(t *testing.T) {
	root := t.TempDir()
	s := New(root)
	if s.Root() != root {
		t.Fatalf("Root: got %q, want %q", s.Root(), root)
	}
}

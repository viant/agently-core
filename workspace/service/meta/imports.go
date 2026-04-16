package meta

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/viant/afs"
	"github.com/viant/afs/storage"
	"gopkg.in/yaml.v3"
)

// ResolveImports recursively resolves $import(...) directives in YAML nodes.
// Scalar imports may target YAML or plain-text files. Plain-text files are
// injected as YAML string scalars so they can be used in any text field.
func ResolveImports(ctx context.Context, fs afs.Service, node *yaml.Node, baseDir string, options ...storage.Option) error {
	if node == nil || fs == nil {
		return nil
	}
	return resolveImports(ctx, fs, node, baseDir, options...)
}

func resolveImports(ctx context.Context, fs afs.Service, node *yaml.Node, baseDir string, options ...storage.Option) error {
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			if err := resolveImports(ctx, fs, child, baseDir, options...); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			if err := processImportNode(ctx, fs, node.Content[i], baseDir, options...); err != nil {
				return err
			}
			if err := processImportNode(ctx, fs, node.Content[i+1], baseDir, options...); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			if err := processImportNode(ctx, fs, child, baseDir, options...); err != nil {
				return err
			}
		}
	case yaml.AliasNode:
		if node.Alias != nil {
			if err := resolveImports(ctx, fs, node.Alias, baseDir, options...); err != nil {
				return err
			}
		}
	}
	return nil
}

func processImportNode(ctx context.Context, fs afs.Service, node *yaml.Node, baseDir string, options ...storage.Option) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" && isImportDirective(node.Value) {
		importPath, err := getImportPath(node.Value)
		if err != nil {
			return err
		}
		fullPath := resolveImportPath(baseDir, importPath)
		data, err := fs.DownloadWithURL(ctx, fullPath, options...)
		if err != nil {
			return err
		}
		imported, err := importedReplacementNode(importPath, data)
		if err != nil {
			return err
		}
		if isYAMLPath(importPath) {
			if err := resolveImports(ctx, fs, imported, filepath.Dir(fullPath), options...); err != nil {
				return err
			}
		}
		*node = *contentNode(imported)
		return nil
	}
	return resolveImports(ctx, fs, node, baseDir, options...)
}

func isImportDirective(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), "$import")
}

func getImportPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !isImportDirective(value) {
		return "", fmt.Errorf("not an import directive: %s", value)
	}
	start := strings.Index(value, "(")
	end := strings.LastIndex(value, ")")
	if start == -1 || end == -1 || start >= end {
		return "", fmt.Errorf("invalid import directive syntax: %s", value)
	}
	pathValue := strings.TrimSpace(value[start+1 : end])
	pathValue = strings.Trim(pathValue, "\"'")
	if strings.TrimSpace(pathValue) == "" {
		return "", fmt.Errorf("empty import path: %s", value)
	}
	return pathValue, nil
}

func resolveImportPath(baseDir, importPath string) string {
	if filepath.IsAbs(importPath) {
		return filepath.Clean(importPath)
	}
	return filepath.Clean(filepath.Join(baseDir, importPath))
}

func isYAMLPath(path string) bool {
	lower := strings.ToLower(strings.TrimSpace(path))
	return strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")
}

func importedReplacementNode(importPath string, data []byte) (*yaml.Node, error) {
	if isYAMLPath(importPath) {
		var node yaml.Node
		if err := yaml.Unmarshal(data, &node); err != nil {
			return nil, err
		}
		return &node, nil
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: string(data)}, nil
}

func contentNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: ""}
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		return node.Content[0]
	}
	return node
}

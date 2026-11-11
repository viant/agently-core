package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// SetCapturedLayout installs an immutable authored layout at startup. The
// existing snapshot supplies checkSource; it checks original file metadata,
// never roles, datasource arguments or a cached authorization decision.
func (h *MetadataHandler) SetCapturedLayout(root, source string, raw []byte, checkSource func(context.Context) error) error {
	if h == nil || root == "" || source == "" || len(raw) == 0 || checkSource == nil {
		return fmt.Errorf("captured layout requires trusted source and generation check")
	}
	var node yaml.Node
	if err := yaml.Unmarshal(raw, &node); err != nil {
		return err
	}
	if err := validateLayoutYAML(&node); err != nil {
		return err
	}
	var layout Layout
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&layout); err != nil {
		return err
	}
	if err := validateLayout(&layout); err != nil {
		return err
	}
	compiled, err := json.Marshal(&layout)
	if err != nil {
		return err
	}
	h.layoutDefault = append([]byte(nil), raw...)
	h.capturedLayoutJSON = compiled
	h.capturedLayoutRoot = root
	h.capturedLayoutSource = source
	h.capturedLayoutCheck = checkSource
	return nil
}

func (h *MetadataHandler) capturedLayout(ctx context.Context) (*Layout, error) {
	if err := h.capturedLayoutCheck(ctx); err != nil {
		return nil, err
	}
	var layout Layout
	if err := json.Unmarshal(h.capturedLayoutJSON, &layout); err != nil {
		return nil, err
	}
	return &layout, nil
}

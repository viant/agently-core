package resource

import (
	"context"
	"encoding/json"

	identity "github.com/viant/agently-core/protocol/resource"
)

// CapturedLayoutSource is an immutable, instance-owned authored shell captured
// by the same native asset producer. It is startup Go wiring, never wire data.
type CapturedLayoutSource struct {
	Root   string
	Source string
	Raw    []byte
	Check  func(context.Context) error
}

// SnapshotLayoutMenuDefinition declares a trusted authored layout file as one
// canonical menu. It does not enumerate other UI files or infer a namespace.
func SnapshotLayoutMenuDefinition(file, uri string) (NativeSnapshotDefinition, error) {
	parsed, err := identity.ParseResourceURI(uri)
	if err != nil || parsed.Kind != "navigation" || parsed.String() != uri || !ValidExtensionPath(file) {
		return NativeSnapshotDefinition{}, identity.ErrResource
	}
	return NativeSnapshotDefinition{URI: uri, Title: "Navigation", FormatVersion: 1, File: file, Load: NativeLayoutMenuLoader}, nil
}

// NativeLayoutMenuLoader compiles the unchanged applications/action tree through
// the existing menu codec. Reads belong to the shared confined asset snapshot;
// no actor, role or grant is captured in this authored source.
func NativeLayoutMenuLoader(ctx context.Context, reader *ExtensionReader, file string) (json.RawMessage, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if reader == nil {
		return nil, identity.ErrResourceDenied
	}
	raw, err := reader.ReadFile(ctx, file)
	if err != nil {
		return nil, err
	}
	return layoutMenuDefinition(raw)
}

// NativeLayoutMenuLoaderWithCapture supplies the same original file bytes to a
// trusted static shell compiler during the producer's single startup read.
func NativeLayoutMenuLoaderWithCapture(capture func([]byte) error) ExtensionLoader {
	return func(ctx context.Context, reader *ExtensionReader, file string) (json.RawMessage, error) {
		if reader == nil {
			return nil, identity.ErrResourceDenied
		}
		raw, err := reader.ReadFile(ctx, file)
		if err != nil {
			return nil, err
		}
		definition, err := layoutMenuDefinition(raw)
		if err != nil {
			return nil, err
		}
		if capture != nil {
			if err := capture(append([]byte(nil), raw...)); err != nil {
				return nil, err
			}
		}
		return definition, nil
	}
}

// SnapshotEmbeddedLayoutMenuDefinition uses explicitly supplied process-owned
// authored bytes. It does not search the filesystem or switch to a later file.
func SnapshotEmbeddedLayoutMenuDefinition(raw []byte, uri string, capture func([]byte) error) (NativeSnapshotDefinition, error) {
	definition, err := SnapshotLayoutMenuDefinition("embedded/layout", uri)
	if err != nil {
		return NativeSnapshotDefinition{}, err
	}
	original := append([]byte(nil), raw...)
	definition.Load = func(ctx context.Context, _ *ExtensionReader, _ string) (json.RawMessage, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		value, err := layoutMenuDefinition(original)
		if err != nil {
			return nil, err
		}
		if capture != nil {
			if err := capture(append([]byte(nil), original...)); err != nil {
				return nil, err
			}
		}
		return value, nil
	}
	return definition, nil
}

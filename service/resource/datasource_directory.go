package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path"
	"strings"

	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	"gopkg.in/yaml.v3"
)

// DatasourceDirectory publishes existing Forge datasource YAML through the
// same confined snapshot inventory used for the other native definitions.
func DatasourceDirectory(namespace, directory string) ExtensionDirectory {
	return ExtensionDirectory{Kind: "datasource", Namespace: namespace, Directory: directory, FormatVersion: 1, Suffixes: []string{".yaml", ".yml"}, Load: NativeDatasourceLoader}
}

func NativeDatasourceLoader(ctx context.Context, reader *ExtensionReader, file string) (json.RawMessage, error) {
	data, err := reader.ReadFile(ctx, file)
	if err != nil {
		return nil, err
	}
	var descriptor dsproto.DataSource
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err = decoder.Decode(&descriptor); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, identity.ErrResourceDenied
	}
	descriptor.ID = strings.TrimSpace(descriptor.ID)
	if descriptor.ID == "" {
		descriptor.ID = strings.TrimSuffix(path.Base(file), path.Ext(file))
	}
	raw, err := json.Marshal(struct {
		SchemaVersion int64               `json:"schemaVersion"`
		DataSource    *dsproto.DataSource `json:"dataSource"`
	}{1, &descriptor})
	if err != nil {
		return nil, err
	}
	if err = ValidateDatasourceDefinition(1, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func ValidateDatasourceDefinition(version int64, raw json.RawMessage) error {
	if version != 1 {
		return identity.ErrResourceDenied
	}
	_, err := DecodeDatasourceDefinition(raw)
	return err
}

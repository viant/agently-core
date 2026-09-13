package style

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

var identityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// A configured workspaceId survives deployment relocation. Otherwise generate
// it once in the workspace, not from a filesystem path or a content revision.
func workspaceIdentity(path string) (string, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	config, err := readFile(root, "config.yaml", 4*1024*1024)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if err == nil {
		var value struct {
			WorkspaceID string `yaml:"workspaceId"`
		}
		if err = yaml.Unmarshal(config, &value); err != nil {
			return "", err
		}
		if value.WorkspaceID != "" {
			id := strings.TrimSpace(value.WorkspaceID)
			if !identityPattern.MatchString(id) {
				return "", fmt.Errorf("invalid workspaceId")
			}
			return id, nil
		}
	}
	existing, err := readFile(root, ".workspace-id", 128)
	if err == nil {
		return validateIdentity(existing)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	id := uuid.NewString()
	file, err := root.OpenFile(".workspace-id", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, fs.ErrExist) {
		existing, err = readFile(root, ".workspace-id", 128)
		if err != nil {
			return "", err
		}
		return validateIdentity(existing)
	}
	if err != nil {
		return "", err
	}
	_, writeErr := file.WriteString(id)
	closeErr := file.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return id, nil
}
func validateIdentity(data []byte) (string, error) {
	id := strings.TrimSpace(string(data))
	if !identityPattern.MatchString(id) {
		return "", fmt.Errorf("invalid workspace identity")
	}
	return id, nil
}

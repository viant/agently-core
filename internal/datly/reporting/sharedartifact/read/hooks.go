package read

import (
	"context"
	"fmt"
	"strings"
)

func (input *Input) Init(context.Context) error {
	switch input.ReadMode {
	case "rows":
	case "byId":
		if strings.TrimSpace(input.ArtifactID) == "" {
			return fmt.Errorf("artifactId is required")
		}
	default:
		return fmt.Errorf("unsupported shared artifact read mode")
	}
	return nil
}

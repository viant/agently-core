package read

import (
	"context"
	"fmt"
	"strings"
)

func (input *Input) Init(context.Context) error {
	if input == nil || !input.Trusted || len(input.ThreadIDs) == 0 {
		return fmt.Errorf("authorized conversation cleanup scope is required")
	}
	for _, id := range input.ThreadIDs {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("cleanup thread identity is empty")
		}
	}
	return nil
}

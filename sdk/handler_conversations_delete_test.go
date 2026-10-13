package sdk

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/data"
)

func TestDeleteConversationErrorStatusUsesTypedPermission(t *testing.T) {
	require.Equal(t, http.StatusForbidden, statusForDeleteConversationError(fmt.Errorf("delete: %w", data.ErrPermissionDenied)))
	require.Equal(t, http.StatusInternalServerError, statusForDeleteConversationError(errors.New("forbidden full drain in protected invocation")))
	require.Equal(t, http.StatusInternalServerError, statusForDeleteConversationError(errors.New("permission denied by internal mutation capability")))
}

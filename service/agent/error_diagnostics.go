package agent

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/viant/jsonrpc"
)

// diagnosticRPCFields keeps untrusted error messages and JSON-RPC data out of
// logs while retaining the numeric outcome needed to locate a failed request.
func diagnosticRPCFields(err error) (code, status int) {
	var rpc *jsonrpc.Error
	if !errors.As(err, &rpc) {
		return 0, 0
	}
	code = rpc.Code
	var data struct {
		Status int `json:"status"`
	}
	if json.Unmarshal(rpc.Data, &data) == nil {
		status = data.Status
	}
	return code, status
}

// Tool selectors can carry caller-specific root metadata after a pipe. The
// catalog lookup strips that suffix, so diagnostics report only the lookup key.
func diagnosticToolPattern(pattern string) string {
	if index := strings.IndexByte(pattern, '|'); index >= 0 {
		pattern = pattern[:index]
	}
	pattern = strings.TrimSpace(pattern)
	if len(pattern) > 160 {
		pattern = pattern[:160]
	}
	return pattern
}

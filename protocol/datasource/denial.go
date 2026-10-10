package datasource

import "fmt"

// UpstreamAuthorizationError preserves numeric MCP error provenance. Producers'
// text, browser payloads and generic status mapping cannot create this decision.
type UpstreamAuthorizationError struct {
	Status int
	Cause  error
}

func (e *UpstreamAuthorizationError) Error() string {
	return fmt.Sprintf("upstream datasource authorization failed (%d)", e.Status)
}
func (e *UpstreamAuthorizationError) Unwrap() error { return e.Cause }

type datasourceDenied struct{}

func (datasourceDenied) Error() string {
	return "Datasource access denied. Adjust the filter or retry."
}
func (datasourceDenied) DatasetScopedDenial() bool { return true }

// ErrDatasourceDenied is issued only after an opted-in host rechecks current
// root access and the original resource/target/descriptor boundary.
var ErrDatasourceDenied error = datasourceDenied{}

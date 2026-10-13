package resource

import (
	"encoding/json"
	"net/url"

	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/types"
)

// Project only descriptors that declare a Core backend onto the host fetch
// route. Native Forge HTTP services retain their exact service and selectors.
// The caller has already verified the selected provider definition and pins.
func projectWindowDatasourceTransport(window *types.Window, descriptors map[string]json.RawMessage) error {
	for id, raw := range descriptors {
		entry, ok := window.DataSource[id]
		if !ok {
			return identity.ErrResourceDenied
		}
		var descriptor dsproto.DataSource
		if json.Unmarshal(raw, &descriptor) != nil {
			return identity.ErrResourceDenied
		}
		if descriptor.Backend == nil {
			continue
		}
		entry.Service = &types.Service{Endpoint: "agentlyAPI", URI: "/v1/api/datasources/" + url.PathEscape(id) + "/fetch", Method: "POST"}
		entry.Selectors = &types.Selectors{Data: "rows", DataInfo: "dataInfo", Metrics: "metrics"}
		window.DataSource[id] = entry
	}
	return nil
}

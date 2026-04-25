package api

// FetchDatasourceInput is the wire request for POST /v1/api/datasources/{id}/fetch.
type FetchDatasourceInput struct {
	ID     string                 `json:"id"`
	Inputs map[string]interface{} `json:"inputs,omitempty"`
	Cache  *DatasourceCacheHints  `json:"cache,omitempty"`
}

// DatasourceCacheHints carries per-call overrides on cache behaviour.
type DatasourceCacheHints struct {
	BypassCache  bool `json:"bypassCache,omitempty"`
	WriteThrough bool `json:"writeThrough,omitempty"`
}

// FetchDatasourceOutput is the wire response.
type FetchDatasourceOutput struct {
	Rows     []map[string]interface{} `json:"rows"`
	DataInfo map[string]interface{}   `json:"dataInfo,omitempty"`
	Cache    *DatasourceCacheMeta     `json:"cache,omitempty"`
}

// DatasourceCacheMeta mirrors protocol/datasource.CacheMeta for HTTP clients
// that do not import the Go protocol packages.
type DatasourceCacheMeta struct {
	Hit        bool   `json:"hit"`
	Stale      bool   `json:"stale,omitempty"`
	FetchedAt  string `json:"fetchedAt"`
	TTLSeconds int    `json:"ttlSeconds,omitempty"`
}

// InvalidateDatasourceCacheInput — DELETE /v1/api/datasources/{id}/cache[?inputsHash=…].
type InvalidateDatasourceCacheInput struct {
	ID         string `json:"id"`
	InputsHash string `json:"inputsHash,omitempty"`
}

// ListLookupRegistryInput — GET /v1/api/lookups/registry?context=<target-kind>:<target-id>.
type ListLookupRegistryInput struct {
	Context string `json:"context"`
}

// LookupRegistryEntry describes a single named-token binding available in a
// render context.
type LookupRegistryEntry struct {
	Name       string            `json:"name"`
	DataSource string            `json:"dataSource"`
	DialogId   string            `json:"dialogId,omitempty"`
	WindowId   string            `json:"windowId,omitempty"`
	Trigger    string            `json:"trigger,omitempty"`
	Required   bool              `json:"required,omitempty"`
	Display    string            `json:"display,omitempty"`
	Token      *TokenFormat      `json:"token,omitempty"`
	Inputs     []LookupParameter `json:"inputs,omitempty"`
	Outputs    []LookupParameter `json:"outputs,omitempty"`
}

// TokenFormat is the client-facing token template set.
type TokenFormat struct {
	Store     string `json:"store,omitempty"`
	Display   string `json:"display,omitempty"`
	ModelForm string `json:"modelForm,omitempty"`
}

// LookupParameter is a wire mirror of forge Parameter — enough to round-trip
// Inputs/Outputs to any client without pulling in forge types.
type LookupParameter struct {
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Name     string `json:"name"`
	Location string `json:"location,omitempty"`
}

// ListLookupRegistryOutput is the wire response.
type ListLookupRegistryOutput struct {
	Entries []LookupRegistryEntry `json:"entries"`
}

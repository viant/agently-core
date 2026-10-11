package api

import (
	"bytes"
	"encoding/json"
	"github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/types"
)

// FetchDatasourceInput is the wire request for POST /v1/api/datasources/{id}/fetch.
type FetchDatasourceInput struct {
	// SelectionBinding is original provenance for optional server NEW delivery preparation.
	SelectionBinding *identity.SelectionBinding `json:"selectionBinding,omitempty"`
	// WindowParameters select entities; the server independently authorizes them.
	WindowParameters map[string]any             `json:"windowParameters,omitempty"`
	ResourceProof    *primitive.ExecutionProof  `json:"resourceProof,omitempty"`
	Target           *types.WindowTarget        `json:"target,omitempty"`
	Resource         *identity.ResolvedResource `json:"resource,omitempty"`
	ID               string                     `json:"id"`
	ConversationID   string                     `json:"conversationId,omitempty"`
	Inputs           map[string]interface{}     `json:"inputs,omitempty"`
	Cache            *DatasourceCacheHints      `json:"cache,omitempty"`
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
	Metrics  map[string]interface{}   `json:"metrics,omitempty"`
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
	Title      string            `json:"title,omitempty"`
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
	Store        string `json:"store,omitempty"`
	Display      string `json:"display,omitempty"`
	ModelForm    string `json:"modelForm,omitempty"`
	QueryInput   string `json:"queryInput,omitempty"`
	ResolveInput string `json:"resolveInput,omitempty"`
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

// UnmarshalJSON preserves exact selection numbers, including IDs outside the
// float64 integer range. Other legacy datasource inputs retain their decoder.
func (in *FetchDatasourceInput) UnmarshalJSON(raw []byte) error {
	type wire FetchDatasourceInput
	var decoded wire
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	var selection struct {
		WindowParameters json.RawMessage `json:"windowParameters"`
	}
	if err := json.Unmarshal(raw, &selection); err != nil {
		return err
	}
	if len(selection.WindowParameters) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(selection.WindowParameters))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded.WindowParameters); err != nil {
			return err
		}
	}
	*in = FetchDatasourceInput(decoded)
	return nil
}

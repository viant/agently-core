package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/types"
	"io"
)

// FetchDatasourceInput is the wire request for POST /v1/api/datasources/{id}/fetch.
type FetchDatasourceInput struct {
	// Owner selects an attached source from the server-selected report definition.
	Owner            *DatasourceOwner      `json:"owner,omitempty"`
	Reference        *identity.ResourceRef `json:"reference,omitempty"`
	ProviderIdentity string                `json:"providerIdentity,omitempty"`
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

// DatasourceOwner is a logical parent identity, not a browser-selected version.
type DatasourceOwner struct {
	URI string `json:"uri"`
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
	Context          string                `json:"context"`
	Owner            *identity.ResourceRef `json:"owner,omitempty"`
	ProviderIdentity string                `json:"providerIdentity,omitempty"`
}

// LookupRegistryEntry describes a single named-token binding available in a
// render context.
type LookupRegistryEntry struct {
	DataSourceResource *identity.ResourceRef `json:"dataSourceResource,omitempty"`
	PickerResource     *identity.ResourceRef `json:"pickerResource,omitempty"`
	ProviderIdentity   string                `json:"providerIdentity,omitempty"`
	Name               string                `json:"name"`
	Title              string                `json:"title,omitempty"`
	DataSource         string                `json:"dataSource"`
	DialogId           string                `json:"dialogId,omitempty"`
	WindowId           string                `json:"windowId,omitempty"`
	Trigger            string                `json:"trigger,omitempty"`
	Required           bool                  `json:"required,omitempty"`
	Display            string                `json:"display,omitempty"`
	Token              *TokenFormat          `json:"token,omitempty"`
	Inputs             []LookupParameter     `json:"inputs,omitempty"`
	Outputs            []LookupParameter     `json:"outputs,omitempty"`
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

// UnmarshalJSON preserves exact numbers in datasource inputs and the complete
// window selection. This decoder is specific to fetch requests; other SDK
// request types keep their existing JSON behavior.
func (in *FetchDatasourceInput) UnmarshalJSON(raw []byte) error {
	type wire FetchDatasourceInput
	var decoded wire
	// Validate the entire value before decoding, including trailing data.
	if !json.Valid(raw) {
		var invalid wire
		return json.Unmarshal(raw, &invalid)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if decoded.Owner != nil {
		strict := json.NewDecoder(bytes.NewReader(raw))
		strict.UseNumber()
		strict.DisallowUnknownFields()
		if err := strict.Decode(&decoded); err != nil {
			return err
		}
	}
	*in = FetchDatasourceInput(decoded)
	return nil
}

// OperateDatasourceInput selects a server-declared action without cache hints.
type OperateDatasourceInput struct {
	Operation string `json:"operation"`
	// Owner selects an attached source from the server-selected report definition.
	Owner            *DatasourceOwner      `json:"owner,omitempty"`
	Reference        *identity.ResourceRef `json:"reference,omitempty"`
	ProviderIdentity string                `json:"providerIdentity,omitempty"`
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
}

// OperateDatasourceOutput preserves the producer result.
type OperateDatasourceOutput struct {
	Result interface{} `json:"result"`
}

// UnmarshalJSON preserves numeric action inputs and rejects browser routing declarations.
func (in *OperateDatasourceInput) UnmarshalJSON(raw []byte) error {
	type wire OperateDatasourceInput
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("unexpected trailing action JSON")
	}
	*in = OperateDatasourceInput(decoded)
	return nil
}

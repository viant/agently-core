package template

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/viant/agently-core/protocol/lookup/overlay"
	identity "github.com/viant/agently-core/protocol/resource"
	"gopkg.in/yaml.v3"
)

func TestTemplateLookupAttachmentsRoundTripAndValidate(t *testing.T) {
	value := &Template{
		ID: "customer-template", Name: "Customer",
		LookupOverlays:  []*overlay.Overlay{{ID: "customer-lookup", Priority: 9, Target: overlay.Target{Kind: "template", ID: "customer-template", SchemaContains: []string{"customerId"}}, Mode: overlay.ModeThreshold, Threshold: 1, Bindings: []overlay.Binding{{Match: overlay.Match{Path: "$.properties.customerId", Type: "string"}, Lookup: overlay.Lookup{DataSource: "customers", DialogId: "customer-picker", Inputs: []overlay.Parameter{{Name: "q", To: "query"}}, Outputs: []overlay.Parameter{{Name: "customerId", From: "row.id"}}, QueryInput: "q", ResolveInput: "id"}, Named: &overlay.NamedToken{Trigger: "/", Name: "customer", Title: "Customer", Required: true, QueryInput: "q", ResolveInput: "id", Store: "${id}", Display: "${name}", ModelForm: "/customer ${name}"}}}}},
		LookupLibraries: []overlay.LibraryReference{{Resource: identity.ResourceRef{URI: "template://catalog/customer-library", Revision: "working"}, ProviderIdentity: "template-provider", ContentFingerprint: strings.Repeat("a", 64)}},
	}
	if err := value.Validate(); err != nil {
		t.Fatal(err)
	}
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fromJSON Template
	if err := json.Unmarshal(jsonBytes, &fromJSON); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(value, &fromJSON) {
		t.Fatalf("JSON attachment round trip changed fields:\n%s", jsonBytes)
	}
	yamlBytes, err := yaml.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fromYAML Template
	if err := yaml.Unmarshal(yamlBytes, &fromYAML); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(value, &fromYAML) {
		t.Fatalf("YAML attachment round trip changed fields:\n%s", yamlBytes)
	}
	if err := value.ValidateForParent(true); err == nil {
		t.Fatal("working library revision accepted for immutable template")
	}
	value.LookupLibraries[0].Resource.Revision = "3"
	if err := value.ValidateForParent(true); err != nil {
		t.Fatalf("stamped library revision rejected: %v", err)
	}
}

func TestTemplateLookupAttachmentsOmittedWhenAbsent(t *testing.T) {
	value := &Template{ID: "plain", Name: "Plain"}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"id":"plain","name":"Plain"}` {
		t.Fatalf("legacy JSON shape changed: %s", encoded)
	}
	yamlBytes, err := yaml.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := yaml.Unmarshal(yamlBytes, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["lookupOverlays"]; ok {
		t.Fatal("empty lookupOverlays emitted")
	}
	if _, ok := decoded["lookupLibraries"]; ok {
		t.Fatal("empty lookupLibraries emitted")
	}
}

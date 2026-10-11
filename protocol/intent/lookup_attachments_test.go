package intent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/viant/agently-core/protocol/lookup/overlay"
	identity "github.com/viant/agently-core/protocol/resource"
	"gopkg.in/yaml.v3"
)

func TestProfileLookupAttachmentsRoundTripAndValidate(t *testing.T) {
	value := &Profile{
		ID:              "account-profile",
		LookupOverlays:  []*overlay.Overlay{{ID: "account-overlay", Target: overlay.Target{Kind: "tool", IDGlob: "workspace-*"}, Bindings: []overlay.Binding{{Match: overlay.Match{FieldName: "accountId"}, Named: &overlay.NamedToken{Name: "account", Required: true, Store: "${id}", Display: "${title}", ModelForm: "Account: ${title}"}}}}},
		LookupLibraries: []overlay.LibraryReference{{Resource: identity.ResourceRef{URI: "template://shared/account", Revision: "working"}, ProviderIdentity: "library-provider", ContentFingerprint: strings.Repeat("b", 64)}},
	}
	if err := value.Validate(); err != nil {
		t.Fatal(err)
	}
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fromJSON Profile
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
	var fromYAML Profile
	if err := yaml.Unmarshal(yamlBytes, &fromYAML); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(value, &fromYAML) {
		t.Fatalf("YAML attachment round trip changed fields:\n%s", yamlBytes)
	}
	if err := value.ValidateForParent(true); err == nil {
		t.Fatal("working library revision accepted for immutable profile")
	}
	value.LookupLibraries[0].Resource.Revision = "4"
	if err := value.ValidateForParent(true); err != nil {
		t.Fatalf("stamped library revision rejected: %v", err)
	}
}

func TestProfileLookupAttachmentsOmittedWhenAbsent(t *testing.T) {
	value := &Profile{ID: "plain-profile"}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"id":"plain-profile"}` {
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

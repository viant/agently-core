package tool

import (
	"context"
	"encoding/json"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/mcp"
	"strings"
	"testing"
)

type exactCodecProvider struct{ version string }

func (p exactCodecProvider) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	return &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{ProtocolVersion: p.version}}, nil
}
func TestExactJSONScopeRefusesUnsafeStructuredCursorButKeepsLegacyAndSafeObjects(t *testing.T) {
	ctx := context.WithValue(context.Background(), exactJSONKey{}, &exactJSONRequest{cursorPath: "nav.next"})
	unsafe := map[string]interface{}{"nav": map[string]interface{}{"next": map[string]interface{}{"ordinal": float64(9223372036854775807)}}}
	if err := validateExactStructuredCursor(ctx, unsafe); err == nil || !strings.Contains(err.Error(), "lossless") {
		t.Fatal("lossy structured cursor admitted", err)
	}
	for _, token := range []interface{}{float64(1), float64(42), map[string]interface{}{"ordinal": float64(1)}, []interface{}{float32(1)}} {
		if err := validateExactStructuredCursor(ctx, map[string]interface{}{"nav": map[string]interface{}{"next": token}}); err == nil {
			t.Fatal("decoded float cursor lacks original numeric precision proof")
		}
	}
	if err := validateExactStructuredCursor(context.Background(), unsafe); err != nil {
		t.Fatal("legacy result decoding changed")
	}
	for _, token := range []interface{}{"opaque", map[string]interface{}{"ordinal": "opaque-part", "valid": true}, json.Number("9223372036854775807")} {
		if err := validateExactStructuredCursor(ctx, map[string]interface{}{"nav": map[string]interface{}{"next": token}}); err != nil {
			t.Fatal("safe or exact structured cursor rejected", err)
		}
	}
}
func TestExactJSONRequestChecksVerifiedCodecWithoutChangingDefaultRequests(t *testing.T) {
	args := map[string]interface{}{"nested": map[string]interface{}{"opaque": json.Number("9223372036854775807")}}
	scope := context.WithValue(context.Background(), exactJSONKey{}, &exactJSONRequest{cursorPath: "nav.next"})
	for _, version := range []string{"2025-06-18", "2026-07-28", ""} {
		mgr, err := manager.New(exactCodecProvider{version})
		if err != nil {
			t.Fatal(err)
		}
		registry, err := NewWithManager(mgr)
		if err != nil {
			t.Fatal(err)
		}
		if err := registry.validateExactJSONRequest(context.Background(), "ordinary", args); err != nil {
			t.Fatal("legacy request contract changed")
		}
		err = registry.validateExactJSONRequest(scope, "ordinary", args)
		if version == "2025-06-18" {
			if err != nil {
				t.Fatal("verified direct HTTP codec rejected", err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "lossless") {
			t.Fatal("unsafe metadata codec allowed dispatch", version, err)
		}
	}
}

func TestExactJSONRequestAllowsProvenFloatRoundTripsAndRefusesChangedNumbers(t *testing.T) {
	for _, number := range []string{"0.25", "1.0", "9007199254740992"} {
		if !numberSurvivesFloatCodec(number) {
			t.Fatal("exact codec value rejected", number)
		}
	}
	for _, number := range []string{"9223372036854775807", "0.1234567890123456789"} {
		if numberSurvivesFloatCodec(number) {
			t.Fatal("lossy codec value admitted", number)
		}
	}
}

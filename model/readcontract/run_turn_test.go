package readcontract_test

import (
	"encoding/json"
	runread "github.com/viant/agently-core/internal/datly/run/read"
	turnread "github.com/viant/agently-core/internal/datly/turn/read"
	"github.com/viant/agently-core/model/run"
	"github.com/viant/agently-core/model/turn"
	"os"
	"reflect"
	"testing"
	"time"
)

var (
	_ *runread.RunRowsView   = (*run.RunRowsView)(nil)
	_ *runread.RunRowsView   = (*run.ActiveRunsView)(nil)
	_ *runread.RunRowsView   = (*run.StaleRunsView)(nil)
	_ *turnread.TurnRowsView = (*turn.TurnRowsView)(nil)
	_ *turnread.TurnRowsView = (*turn.ActiveTurnsView)(nil)
	_ *turnread.TurnRowsView = (*turn.TurnLookupView)(nil)
	_ *turnread.TurnRowsView = (*turn.QueuedTurnView)(nil)
)

type golden struct {
	Zero, Populated map[string]json.RawMessage
	Types           map[string]string
}

// The fixture was captured from the seven pre-alias public shapes, normalizing
// only the initial JSON-name case to the authoritative DQL lower-camel policy.
func TestRunTurnGeneratedAliasesPreservePublicWire(t *testing.T) {
	raw, err := os.ReadFile("testdata/run_turn_wire.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]golden
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, family string
		value        any
	}{{"RunRows", "RunRowsView", &run.RunRowsView{}}, {"ActiveRuns", "RunRowsView", &run.ActiveRunsView{}}, {"StaleRuns", "RunRowsView", &run.StaleRunsView{}}, {"TurnRows", "TurnRowsView", &turn.TurnRowsView{}}, {"ActiveTurns", "TurnRowsView", &turn.ActiveTurnsView{}}, {"TurnLookup", "TurnRowsView", &turn.TurnLookupView{}}, {"QueuedTurn", "TurnRowsView", &turn.QueuedTurnView{}}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expected := fixtures[tc.family]
			assertWire(t, tc.value, expected.Zero)
			value := reflect.ValueOf(tc.value).Elem()
			explicitZero := make(map[string]json.RawMessage, len(expected.Zero))
			for key, raw := range expected.Zero {
				explicitZero[key] = raw
			}
			for index := 0; index < value.NumField(); index++ {
				field := value.Type().Field(index)
				if field.Type.Kind() != reflect.Pointer {
					continue
				}
				value.Field(index).Set(reflect.New(field.Type.Elem()))
				key := field.Tag.Get("json")
				if key != "-" {
					encoded, err := json.Marshal(value.Field(index).Interface())
					if err != nil {
						t.Fatal(err)
					}
					explicitZero[key] = encoded
				}
			}
			assertWire(t, tc.value, explicitZero)
			for index := 0; index < value.NumField(); index++ {
				field := value.Type().Field(index)
				key := field.Tag.Get("json")
				if key != "-" {
					if expected.Types[key] != field.Type.String() {
						t.Fatalf("%s type changed: %s -> %s", key, expected.Types[key], field.Type)
					}
				}
				populate(value.Field(index))
			}
			assertWire(t, tc.value, expected.Populated)
		})
	}
	if reflect.TypeFor[turn.QueuedTurnsView]().NumField() != 2 {
		t.Fatal("narrow queued-list contract was broadened")
	}
}
func assertWire(t *testing.T, value any, expected map[string]json.RawMessage) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var actual map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("wire keys/values/null/zero changed: %s", encoded)
	}
}
func populate(value reflect.Value) {
	switch value.Kind() {
	case reflect.Pointer:
		value.Set(reflect.New(value.Type().Elem()))
		populate(value.Elem())
	case reflect.String:
		value.SetString("ORANGE-42 café 界")
	case reflect.Int, reflect.Int64:
		value.SetInt(7)
	case reflect.Float64:
		value.SetFloat(1.25)
	case reflect.Bool:
		value.SetBool(true)
	case reflect.Struct:
		if value.Type() == reflect.TypeOf(time.Time{}) {
			value.Set(reflect.ValueOf(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
		}
	}
}

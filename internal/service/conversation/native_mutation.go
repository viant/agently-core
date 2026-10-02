package conversation

import (
	"fmt"
	"reflect"
	"strings"
)

// nativePresence records the mapped input presence before Datly lifecycle hooks
// assign defaults. Keys use case-insensitive names for public ID/URI acronyms.
func nativePresence(row any) map[string]bool {
	result := map[string]bool{}
	value := reflect.ValueOf(row)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return result
	}
	has := value.Elem().FieldByName("Has")
	if !has.IsValid() || has.IsNil() {
		return result
	}
	marker := has.Elem()
	for i := 0; i < marker.NumField(); i++ {
		if marker.Field(i).Kind() == reflect.Bool {
			result[strings.ToLower(marker.Type().Field(i).Name)] = marker.Field(i).Bool()
		}
	}
	return result
}

// applyNativeMutation preserves the public input object's identity. It copies
// supplied fields and fields newly assigned by the successful writer lifecycle.
// Mapping-only identity markers are not added to the caller's sparse patch.
func applyNativeMutation[Row any](destination any, rows []*Row, initial map[string]bool) error {
	if len(rows) != 1 || rows[0] == nil {
		return fmt.Errorf("native mutation returned %d rows", len(rows))
	}
	source := reflect.ValueOf(rows[0]).Elem()
	target := reflect.ValueOf(destination).Elem()
	sourceHas := source.FieldByName("Has")
	if !sourceHas.IsValid() || sourceHas.IsNil() {
		return nil
	}
	marker := sourceHas.Elem()
	fields := map[string]int{}
	for i := 0; i < source.NumField(); i++ {
		fields[strings.ToLower(source.Type().Field(i).Name)] = i
	}
	markers := map[string]bool{}
	for i := 0; i < marker.NumField(); i++ {
		if marker.Field(i).Kind() == reflect.Bool {
			markers[strings.ToLower(marker.Type().Field(i).Name)] = marker.Field(i).Bool()
		}
	}
	targetHas := target.FieldByName("Has")
	type assignment struct {
		field reflect.Value
		value reflect.Value
		name  string
	}
	assignments := []assignment{}
	for i := 0; i < target.NumField(); i++ {
		name := target.Type().Field(i).Name
		key := strings.ToLower(name)
		if name == "Has" || !markers[key] {
			continue
		}
		var targetFlag reflect.Value
		if !targetHas.IsNil() {
			targetFlag = targetHas.Elem().FieldByName(name)
		}
		supplied := targetFlag.IsValid() && targetFlag.Bool()
		if !supplied && initial[key] {
			continue
		}
		sourceIndex, exists := fields[key]
		if !exists {
			continue
		}
		field, value := target.Field(i), source.Field(sourceIndex)
		if !value.Type().AssignableTo(field.Type()) {
			return fmt.Errorf("native mutation field %s has type %s, expected %s", name, value.Type(), field.Type())
		}
		assignments = append(assignments, assignment{field: field, value: value, name: name})
	}
	for _, assignment := range assignments {
		assignment.field.Set(assignment.value)
		if targetHas.IsNil() {
			targetHas.Set(reflect.New(targetHas.Type().Elem()))
		}
		flag := targetHas.Elem().FieldByName(assignment.name)
		if flag.IsValid() && flag.CanSet() {
			flag.SetBool(true)
		}
	}
	return nil
}

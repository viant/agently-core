package invariant

import (
	"fmt"
	"reflect"
	"strings"
)

// Native writers share physical tables but do not own protocol identity,
// projection or lease columns. Inspect original suppliedness before native
// insertion defaults mark their own generated setters.
func ValidateNativeProtocolFields(entity any, existing bool) error {
	row := reflect.ValueOf(entity)
	if !row.IsValid() || row.Kind() != reflect.Pointer || row.IsNil() {
		return nil
	}
	row = row.Elem()
	has := row.FieldByName("Has")
	if !has.IsValid() || has.IsNil() {
		return nil
	}
	has = has.Elem()
	for i := 0; i < row.NumField(); i++ {
		name := row.Type().Field(i).Name
		if !strings.HasPrefix(name, "Protocol") {
			continue
		}
		marked := has.FieldByName(name)
		if !marked.IsValid() || marked.Kind() != reflect.Bool || !marked.Bool() {
			continue
		}
		if existing {
			return fmt.Errorf("native writer cannot modify protocol field %s", name)
		}
		value := row.Field(i)
		if value.Kind() == reflect.Pointer {
			if value.IsNil() {
				continue
			}
			value = value.Elem()
		}
		switch value.Kind() {
		case reflect.Bool:
			if !value.Bool() {
				continue
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if value.Int() == 0 {
				continue
			}
		}
		return fmt.Errorf("native insertion cannot supply protocol field %s", name)
	}
	return nil
}

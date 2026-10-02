package invariant

import (
	"fmt"
	"reflect"
	"strings"
)

// ValidateOrphanDetach checks business-only maintenance shape. The canonical
// generated writer still owns identity, Previous, validation and DML. This
// helper neither reads rows nor constructs SQL.
func ValidateOrphanDetach(entity, previous any, column string, allowedColumns []string, keyFields ...string) error {
	value := reflect.ValueOf(entity)
	prior := reflect.ValueOf(previous)
	if value.Kind() != reflect.Pointer || value.IsNil() || prior.Kind() != reflect.Pointer || prior.IsNil() {
		return fmt.Errorf("orphan detach requires an existing row")
	}
	value = value.Elem()
	if value.Kind() != reflect.Struct || prior.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("orphan maintenance requires typed row shapes")
	}
	if deletion := value.FieldByName("ShouldDelete"); deletion.IsValid() && deletion.Kind() == reflect.Bool && deletion.Bool() {
		return fmt.Errorf("orphan detach cannot delete rows")
	}
	prior = prior.Elem()
	allowed := false
	for _, candidate := range allowedColumns {
		if column == candidate {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("orphan detach column %q is not permitted", column)
	}
	fieldName := ""
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		if strings.Split(field.Tag.Get("sqlx"), ",")[0] == column {
			fieldName = field.Name
			break
		}
	}
	if fieldName == "" {
		return fmt.Errorf("orphan detach column %q is unavailable", column)
	}
	field := value.FieldByName(fieldName)
	if field.Kind() != reflect.Pointer || !field.IsNil() {
		return fmt.Errorf("orphan detach only permits a nil reference")
	}
	markers := value.FieldByName("Has")
	if markers.Kind() != reflect.Pointer || markers.IsNil() {
		return fmt.Errorf("orphan detach requires sparse presence")
	}
	markers = markers.Elem()
	permitted := map[string]bool{fieldName: true}
	for _, key := range keyFields {
		permitted[key] = true
		currentKey, previousKey := value.FieldByName(key), prior.FieldByName(key)
		if !currentKey.IsValid() || !previousKey.IsValid() || !reflect.DeepEqual(currentKey.Interface(), previousKey.Interface()) {
			return fmt.Errorf("orphan detach cannot change identity")
		}
		if marker := markers.FieldByName(key); !marker.IsValid() || marker.Kind() != reflect.Bool || !marker.Bool() {
			return fmt.Errorf("orphan detach requires complete identity presence")
		}
	}
	if marker := markers.FieldByName(fieldName); !marker.IsValid() || marker.Kind() != reflect.Bool || !marker.Bool() {
		return fmt.Errorf("orphan detach requires explicit nil presence")
	}
	for i := 0; i < markers.NumField(); i++ {
		if markers.Field(i).Kind() == reflect.Bool && markers.Field(i).Bool() && !permitted[markers.Type().Field(i).Name] {
			return fmt.Errorf("orphan detach permits only identity and one nil reference")
		}
	}
	return nil
}

// ValidateOrphanDelete applies the dedicated identity-only existing-row shape.
func ValidateOrphanDelete(entity, previous any, keyFields ...string) error {
	value := reflect.ValueOf(entity)
	prior := reflect.ValueOf(previous)
	if value.Kind() != reflect.Pointer || value.IsNil() || prior.Kind() != reflect.Pointer || prior.IsNil() {
		return fmt.Errorf("orphan deletion requires an existing row")
	}
	value = value.Elem()
	if value.Kind() != reflect.Struct || prior.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("orphan maintenance requires typed row shapes")
	}
	prior = prior.Elem()
	flag := value.FieldByName("ShouldDelete")
	markers := value.FieldByName("Has")
	if !flag.IsValid() || flag.Kind() != reflect.Bool || !flag.Bool() || markers.Kind() != reflect.Pointer || markers.IsNil() {
		return fmt.Errorf("orphan deletion requires explicit deletion presence")
	}
	markers = markers.Elem()
	marked := markers.FieldByName("ShouldDelete")
	if !marked.IsValid() || marked.Kind() != reflect.Bool || !marked.Bool() {
		return fmt.Errorf("orphan deletion requires explicit deletion presence")
	}
	allowed := map[string]bool{"ShouldDelete": true}
	for _, key := range keyFields {
		allowed[key] = true
		current, old := value.FieldByName(key), prior.FieldByName(key)
		mark := markers.FieldByName(key)
		if !current.IsValid() || !old.IsValid() || !reflect.DeepEqual(current.Interface(), old.Interface()) || !mark.IsValid() || mark.Kind() != reflect.Bool || !mark.Bool() {
			return fmt.Errorf("orphan deletion requires unchanged complete identity")
		}
	}
	for i := 0; i < markers.NumField(); i++ {
		if markers.Field(i).Kind() == reflect.Bool && markers.Field(i).Bool() && !allowed[markers.Type().Field(i).Name] {
			return fmt.Errorf("orphan deletion permits only identity and deletion marker")
		}
	}
	return nil
}

package data

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

// mapDataDTO translates public facade shapes to canonical generated shapes.
// Presence markers and nullable values remain explicit, including ID/Id names.
func mapDataDTO[T any](source any) (*T, error) {
	if source == nil {
		return nil, nil
	}
	value := reflect.ValueOf(source)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return nil, nil
	}
	target := new(T)
	if err := copyDataValue(reflect.ValueOf(target).Elem(), value); err != nil {
		return nil, err
	}
	return target, nil
}
func mapDataDTOs[T any, S any](source []*S) ([]*T, error) {
	result := make([]*T, 0, len(source))
	for _, row := range source {
		mapped, err := mapDataDTO[T](row)
		if err != nil {
			return nil, err
		}
		if mapped != nil {
			result = append(result, mapped)
		}
	}
	return result, nil
}
func copyDataValue(target, source reflect.Value) error {
	for source.Kind() == reflect.Pointer || source.Kind() == reflect.Interface {
		if source.IsNil() {
			target.Set(reflect.Zero(target.Type()))
			return nil
		}
		source = source.Elem()
	}
	if target.Kind() == reflect.Pointer {
		if target.IsNil() {
			target.Set(reflect.New(target.Type().Elem()))
		}
		return copyDataValue(target.Elem(), source)
	}
	if source.Type().AssignableTo(target.Type()) && (target.Kind() != reflect.Struct || target.Type() == reflect.TypeFor[time.Time]()) && target.Kind() != reflect.Slice {
		target.Set(source)
		return nil
	}
	if target.Kind() == reflect.String && source.Kind() == reflect.Slice && source.Type().Elem().Kind() == reflect.Uint8 {
		target.SetString(string(source.Bytes()))
		return nil
	}
	if target.Kind() == reflect.Slice && target.Type().Elem().Kind() == reflect.Uint8 && source.Kind() == reflect.String {
		target.SetBytes([]byte(source.String()))
		return nil
	}
	if target.Kind() == reflect.Slice && source.Kind() == reflect.Slice {
		if source.IsNil() {
			target.Set(reflect.Zero(target.Type()))
			return nil
		}
		target.Set(reflect.MakeSlice(target.Type(), source.Len(), source.Len()))
		for i := 0; i < source.Len(); i++ {
			if err := copyDataValue(target.Index(i), source.Index(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if target.Kind() == reflect.Struct && source.Kind() == reflect.Struct {
		for i := 0; i < target.NumField(); i++ {
			tf := target.Type().Field(i)
			if !target.Field(i).CanSet() {
				continue
			}
			for j := 0; j < source.NumField(); j++ {
				sf := source.Type().Field(j)
				if !sf.IsExported() {
					continue
				}
				if strings.EqualFold(tf.Name, sf.Name) || dataColumnMatches(tf, sf) {
					if err := copyDataValue(target.Field(i), source.Field(j)); err != nil {
						return fmt.Errorf("map %s: %w", tf.Name, err)
					}
					break
				}
			}
		}
		return nil
	}
	if source.Type().ConvertibleTo(target.Type()) {
		target.Set(source.Convert(target.Type()))
		return nil
	}
	return fmt.Errorf("cannot map %s to %s", source.Type(), target.Type())
}
func dataColumnMatches(a, b reflect.StructField) bool {
	left := strings.Split(a.Tag.Get("sqlx"), ",")[0]
	right := strings.Split(b.Tag.Get("sqlx"), ",")[0]
	return left != "" && left != "-" && left == right
}

// applyDataMutationResult retains public logical fields which have no stored
// column or generated presence marker, such as tool ResponseOverflow.
func applyDataMutationResult(target, source reflect.Value) error {
	for source.Kind() == reflect.Pointer {
		if source.IsNil() {
			return fmt.Errorf("nil mutation result")
		}
		source = source.Elem()
	}
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	for i := 0; i < target.NumField(); i++ {
		tf := target.Type().Field(i)
		if !target.Field(i).CanSet() {
			continue
		}
		if tf.Name != "Has" && strings.Split(tf.Tag.Get("sqlx"), ",")[0] == "-" {
			continue
		}
		for j := 0; j < source.NumField(); j++ {
			sf := source.Type().Field(j)
			if strings.EqualFold(tf.Name, sf.Name) || dataColumnMatches(tf, sf) {
				if err := copyDataValue(target.Field(i), source.Field(j)); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

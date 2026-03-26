package confm

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-viper/mapstructure/v2"
)

// DecodeStringToMap returns a DecodeHookFunc that converts a string to a map.
//
// Resolution order:
//  1. JSON object string (e.g. `{"k":"v"}`) → works for any map type
//  2. k=v,k=v format → only for map[string]string targets
//  3. Empty string → zero-value map of the target type
func DecodeStringToMap() mapstructure.DecodeHookFunc {
	return mapstructure.DecodeHookFuncType(func(f reflect.Type, t reflect.Type, data interface{}) (interface{}, error) {
		if f.Kind() != reflect.String || t.Kind() != reflect.Map {
			return data, nil
		}

		raw := data.(string)
		if raw == "" {
			return reflect.MakeMap(t).Interface(), nil
		}

		// Try JSON first — works for all map types.
		if raw[0] == '{' {
			var m any
			if err := json.Unmarshal([]byte(raw), &m); err == nil {
				return m, nil
			}
		}

		// Fallback: k=v,k=v — only for map[string]string.
		if t.Key().Kind() != reflect.String || t.Elem().Kind() != reflect.String {
			return data, nil
		}
		pairs := strings.Split(raw, ",")
		m := make(map[string]string, len(pairs))
		for _, pair := range pairs {
			key, value, found := strings.Cut(pair, "=")
			if !found {
				return nil, fmt.Errorf("invalid key-value pair: %s", pair)
			}
			m[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
		return m, nil
	})
}

// StringToSliceWithBracketHookFunc returns a DecodeHookFunc that converts a string to a slice of strings.
// Useful when configuration values are provided as JSON arrays in string form, but need to be parsed into slices.
// The string is expected to be a JSON array.
// If the string is empty, an empty slice is returned.
// If the string cannot be parsed as a JSON array, the original data is returned unchanged.
func StringToSliceWithBracketHookFunc() mapstructure.DecodeHookFunc {
	return func(f reflect.Kind, t reflect.Kind, data interface{}) (interface{}, error) {
		if f != reflect.String || t != reflect.Slice {
			return data, nil
		}

		raw := data.(string)
		if raw == "" {
			return []string{}, nil
		}
		var result any
		err := json.Unmarshal([]byte(raw), &result)
		if err != nil {
			return data, nil
		}

		// Verify that the result matches the target (slice)
		if reflect.TypeOf(result).Kind() != t {
			return data, nil
		}
		return result, nil
	}
}

// StringToStructHookFunc returns a DecodeHookFunc that converts a string to a struct.
// Useful for parsing configuration values that are provided as JSON strings but need to be converted to structs.
// The string is expected to be a JSON object that can be unmarshaled into the target struct.
// If the string is empty, a new instance of the target struct is returned.
// If the string cannot be parsed as a JSON object, the original data is returned unchanged.
func StringToStructHookFunc() mapstructure.DecodeHookFunc {
	return func(f reflect.Type, t reflect.Type, data interface{}) (interface{}, error) {
		if f.Kind() != reflect.String ||
			(t.Kind() != reflect.Struct && !(t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Struct)) {
			return data, nil
		}
		raw := data.(string)
		var val reflect.Value
		// Struct or the pointer to a struct
		if t.Kind() == reflect.Struct {
			val = reflect.New(t)
		} else {
			val = reflect.New(t.Elem())
		}

		if raw == "" {
			return val.Interface(), nil
		}
		var m map[string]interface{}
		err := json.Unmarshal([]byte(raw), &m)
		if err != nil {
			return data, nil
		}
		return m, nil
	}
}

// CompositeDecodeHook composes all decode hooks into a single DecodeHookFunc.
func CompositeDecodeHook() mapstructure.DecodeHookFunc {
	return mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToTimeDurationHookFunc(),
		DecodeStringToMap(),
		StringToStructHookFunc(),
		StringToSliceWithBracketHookFunc(),
	)
}

package confm

import (
	"reflect"
	"testing"

	"github.com/go-viper/mapstructure/v2"
)

// --- DecodeStringToMap ---

func TestDecodeStringToMap_ValidPairs(t *testing.T) {
	hook := DecodeStringToMap()
	fn := hook.(mapstructure.DecodeHookFuncType)

	strType := reflect.TypeOf("")
	mapType := reflect.TypeOf(map[string]string{})

	result, err := fn(strType, mapType, "a=1,b=2,c=3")
	if err != nil {
		t.Fatal(err)
	}
	m := result.(map[string]string)
	if m["a"] != "1" || m["b"] != "2" || m["c"] != "3" {
		t.Errorf("got %v", m)
	}
}

func TestDecodeStringToMap_TrimSpaces(t *testing.T) {
	hook := DecodeStringToMap()
	fn := hook.(mapstructure.DecodeHookFuncType)

	strType := reflect.TypeOf("")
	mapType := reflect.TypeOf(map[string]string{})

	result, err := fn(strType, mapType, " key = value , foo = bar ")
	if err != nil {
		t.Fatal(err)
	}
	m := result.(map[string]string)
	if m["key"] != "value" || m["foo"] != "bar" {
		t.Errorf("got %v", m)
	}
}

func TestDecodeStringToMap_EmptyString(t *testing.T) {
	hook := DecodeStringToMap()
	fn := hook.(mapstructure.DecodeHookFuncType)

	strType := reflect.TypeOf("")
	mapType := reflect.TypeOf(map[string]string{})

	result, err := fn(strType, mapType, "")
	if err != nil {
		t.Fatal(err)
	}
	m := result.(map[string]string)
	if len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
}

func TestDecodeStringToMap_MissingEquals(t *testing.T) {
	hook := DecodeStringToMap()
	fn := hook.(mapstructure.DecodeHookFuncType)

	strType := reflect.TypeOf("")
	mapType := reflect.TypeOf(map[string]string{})

	_, err := fn(strType, mapType, "no-equals")
	if err == nil {
		t.Error("expected error for missing =")
	}
}

func TestDecodeStringToMap_ValueWithEquals(t *testing.T) {
	hook := DecodeStringToMap()
	fn := hook.(mapstructure.DecodeHookFuncType)

	strType := reflect.TypeOf("")
	mapType := reflect.TypeOf(map[string]string{})

	result, err := fn(strType, mapType, "url=http://host?a=1")
	if err != nil {
		t.Fatal(err)
	}
	m := result.(map[string]string)
	if m["url"] != "http://host?a=1" {
		t.Errorf("got %v", m)
	}
}

func TestDecodeStringToMap_SkipsNonStringSource(t *testing.T) {
	hook := DecodeStringToMap()
	fn := hook.(mapstructure.DecodeHookFuncType)

	intType := reflect.TypeOf(0)
	mapType := reflect.TypeOf(map[string]string{})

	result, err := fn(intType, mapType, 42)
	if err != nil {
		t.Fatal(err)
	}
	if result != 42 {
		t.Errorf("expected passthrough, got %v", result)
	}
}

func TestDecodeStringToMap_SkipsNonStringStringMap_KV(t *testing.T) {
	hook := DecodeStringToMap()
	fn := hook.(mapstructure.DecodeHookFuncType)

	strType := reflect.TypeOf("")
	mapIntType := reflect.TypeOf(map[string]int{})

	// k=v format is only for map[string]string; other map types pass through.
	result, err := fn(strType, mapIntType, "a=1")
	if err != nil {
		t.Fatal(err)
	}
	if result != "a=1" {
		t.Errorf("expected passthrough, got %v", result)
	}
}

func TestDecodeStringToMap_JSON_MapStringAny(t *testing.T) {
	hook := DecodeStringToMap()
	fn := hook.(mapstructure.DecodeHookFuncType)

	strType := reflect.TypeOf("")
	mapAnyType := reflect.TypeOf(map[string]any{})

	result, err := fn(strType, mapAnyType, `{"key":"val","n":1}`)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	if m["key"] != "val" {
		t.Errorf("m[key] = %v", m["key"])
	}
	if m["n"] != float64(1) {
		t.Errorf("m[n] = %v", m["n"])
	}
}

func TestDecodeStringToMap_JSON_MapStringString(t *testing.T) {
	hook := DecodeStringToMap()
	fn := hook.(mapstructure.DecodeHookFuncType)

	strType := reflect.TypeOf("")
	mapType := reflect.TypeOf(map[string]string{})

	// JSON takes priority over k=v for map[string]string too.
	result, err := fn(strType, mapType, `{"a":"1","b":"2"}`)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	if m["a"] != "1" || m["b"] != "2" {
		t.Errorf("got %v", m)
	}
}

func TestDecodeStringToMap_EmptyString_MapStringAny(t *testing.T) {
	hook := DecodeStringToMap()
	fn := hook.(mapstructure.DecodeHookFuncType)

	strType := reflect.TypeOf("")
	mapAnyType := reflect.TypeOf(map[string]any{})

	result, err := fn(strType, mapAnyType, "")
	if err != nil {
		t.Fatal(err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
}

// --- StringToSliceWithBracketHookFunc ---

func TestStringToSlice_JSONArray(t *testing.T) {
	hook := StringToSliceWithBracketHookFunc()
	fn := hook.(func(reflect.Kind, reflect.Kind, interface{}) (interface{}, error))

	result, err := fn(reflect.String, reflect.Slice, `["a","b","c"]`)
	if err != nil {
		t.Fatal(err)
	}
	slice, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(slice) != 3 {
		t.Errorf("len = %d, want 3", len(slice))
	}
}

func TestStringToSlice_EmptyString(t *testing.T) {
	hook := StringToSliceWithBracketHookFunc()
	fn := hook.(func(reflect.Kind, reflect.Kind, interface{}) (interface{}, error))

	result, err := fn(reflect.String, reflect.Slice, "")
	if err != nil {
		t.Fatal(err)
	}
	slice := result.([]string)
	if len(slice) != 0 {
		t.Errorf("expected empty slice, got %v", slice)
	}
}

func TestStringToSlice_InvalidJSON_Passthrough(t *testing.T) {
	hook := StringToSliceWithBracketHookFunc()
	fn := hook.(func(reflect.Kind, reflect.Kind, interface{}) (interface{}, error))

	result, err := fn(reflect.String, reflect.Slice, "not-json")
	if err != nil {
		t.Fatal(err)
	}
	if result != "not-json" {
		t.Errorf("expected passthrough, got %v", result)
	}
}

func TestStringToSlice_JSONObject_Passthrough(t *testing.T) {
	hook := StringToSliceWithBracketHookFunc()
	fn := hook.(func(reflect.Kind, reflect.Kind, interface{}) (interface{}, error))

	// A JSON object is not a slice — should pass through.
	result, err := fn(reflect.String, reflect.Slice, `{"key":"val"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Error("expected passthrough, got nil")
	}
}

// structHookFn is the function signature returned by StringToStructHookFunc.
type structHookFn = func(reflect.Type, reflect.Type, interface{}) (interface{}, error)

// --- StringToStructHookFunc ---

func TestStringToStruct_JSONObject(t *testing.T) {
	type inner struct {
		X int    `json:"x"`
		Y string `json:"y"`
	}

	hook := StringToStructHookFunc()
	fn := hook.(structHookFn)

	strType := reflect.TypeOf("")
	structType := reflect.TypeOf(inner{})

	result, err := fn(strType, structType, `{"x":10,"y":"hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	// The hook returns a map[string]any for mapstructure to decode further.
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	if m["x"] != float64(10) || m["y"] != "hello" {
		t.Errorf("got %v", m)
	}
}

func TestStringToStruct_EmptyString_ReturnsZeroValue(t *testing.T) {
	type inner struct {
		X int `json:"x"`
	}

	hook := StringToStructHookFunc()
	fn := hook.(structHookFn)

	strType := reflect.TypeOf("")
	structType := reflect.TypeOf(inner{})

	result, err := fn(strType, structType, "")
	if err != nil {
		t.Fatal(err)
	}
	// Should return a *inner (pointer to zero-value struct), not a reflect.Value.
	ptr, ok := result.(*inner)
	if !ok {
		t.Fatalf("expected *inner, got %T", result)
	}
	if ptr.X != 0 {
		t.Errorf("X = %d, want 0", ptr.X)
	}
}

func TestStringToStruct_InvalidJSON_Passthrough(t *testing.T) {
	type inner struct{ X int }

	hook := StringToStructHookFunc()
	fn := hook.(structHookFn)

	strType := reflect.TypeOf("")
	structType := reflect.TypeOf(inner{})

	result, err := fn(strType, structType, "not-json")
	if err != nil {
		t.Fatal(err)
	}
	if result != "not-json" {
		t.Errorf("expected passthrough, got %v", result)
	}
}

func TestStringToStruct_NonStringSource_Passthrough(t *testing.T) {
	type inner struct{ X int }

	hook := StringToStructHookFunc()
	fn := hook.(structHookFn)

	intType := reflect.TypeOf(0)
	structType := reflect.TypeOf(inner{})

	result, err := fn(intType, structType, 42)
	if err != nil {
		t.Fatal(err)
	}
	if result != 42 {
		t.Errorf("expected passthrough, got %v", result)
	}
}

// --- CompositeDecodeHook integration ---

func TestCompositeHook_MapstructureDecode(t *testing.T) {
	type cfg struct {
		Headers map[string]string `mapstructure:"headers"`
		Tags    []string          `mapstructure:"tags"`
		Debug   bool              `mapstructure:"debug"`
	}

	input := map[string]any{
		"headers": "X-A=1,X-B=2",
		"tags":    `["go","rust"]`,
		"debug":   true,
	}

	var out cfg
	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		DecodeHook: CompositeDecodeHook(),
		Result:     &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := dec.Decode(input); err != nil {
		t.Fatal(err)
	}

	if out.Headers["X-A"] != "1" || out.Headers["X-B"] != "2" {
		t.Errorf("Headers = %v", out.Headers)
	}
	if len(out.Tags) != 2 || out.Tags[0] != "go" {
		t.Errorf("Tags = %v", out.Tags)
	}
	if !out.Debug {
		t.Error("Debug should be true")
	}
}

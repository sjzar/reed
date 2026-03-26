package confm

import (
	"reflect"
	"sort"
	"testing"
)

func TestGetStructKeys_FlatStruct(t *testing.T) {
	type flat struct {
		Name string `mapstructure:"name"`
		Port int    `mapstructure:"port"`
	}

	keys := GetStructKeys(reflect.TypeOf(flat{}), "mapstructure", "squash")
	sort.Strings(keys)

	want := []string{"name", "port"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
}

func TestGetStructKeys_NestedStruct(t *testing.T) {
	type inner struct {
		Addr string `mapstructure:"addr"`
	}
	type outer struct {
		Name  string `mapstructure:"name"`
		Inner inner  `mapstructure:"inner"`
	}

	keys := GetStructKeys(reflect.TypeOf(outer{}), "mapstructure", "squash")
	sort.Strings(keys)

	want := []string{"inner.addr", "name"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
}

func TestGetStructKeys_SquashedEmbed(t *testing.T) {
	type base struct {
		Host string `mapstructure:"host"`
	}
	type outer struct {
		base `mapstructure:",squash"`
		Port int `mapstructure:"port"`
	}

	keys := GetStructKeys(reflect.TypeOf(outer{}), "mapstructure", "squash")
	sort.Strings(keys)

	// Squashed fields should not add a prefix level.
	want := []string{"host", "port"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
}

func TestGetStructKeys_NoTag_UsesLowercaseName(t *testing.T) {
	type noTag struct {
		MyField string
	}

	keys := GetStructKeys(reflect.TypeOf(noTag{}), "mapstructure", "squash")
	if len(keys) != 1 || keys[0] != "myfield" {
		t.Errorf("keys = %v, want [myfield]", keys)
	}
}

func TestGetStructKeys_PointerField(t *testing.T) {
	type inner struct {
		Val string `mapstructure:"val"`
	}
	type outer struct {
		Ptr *inner `mapstructure:"ptr"`
	}

	keys := GetStructKeys(reflect.TypeOf(outer{}), "mapstructure", "squash")
	if len(keys) != 1 || keys[0] != "ptr.val" {
		t.Errorf("keys = %v, want [ptr.val]", keys)
	}
}

func TestGetStructKeys_DeeplyNested(t *testing.T) {
	type l3 struct {
		Z string `mapstructure:"z"`
	}
	type l2 struct {
		L3 l3 `mapstructure:"l3"`
	}
	type l1 struct {
		L2 l2 `mapstructure:"l2"`
	}

	keys := GetStructKeys(reflect.TypeOf(l1{}), "mapstructure", "squash")
	if len(keys) != 1 || keys[0] != "l2.l3.z" {
		t.Errorf("keys = %v, want [l2.l3.z]", keys)
	}
}

// --- ValidateMissingRequiredKeys ---

func TestValidateMissingRequiredKeys_DetectsZero(t *testing.T) {
	type cfg struct {
		Name string `mapstructure:"name" validate:"required"`
		Port int    `mapstructure:"port"`
	}

	missing := ValidateMissingRequiredKeys(cfg{}, "mapstructure", "squash")
	if len(missing) != 1 || missing[0] != "name" {
		t.Errorf("missing = %v, want [name]", missing)
	}
}

func TestValidateMissingRequiredKeys_AllSet(t *testing.T) {
	type cfg struct {
		Name string `mapstructure:"name" validate:"required"`
	}

	missing := ValidateMissingRequiredKeys(cfg{Name: "ok"}, "mapstructure", "squash")
	if len(missing) != 0 {
		t.Errorf("missing = %v, want []", missing)
	}
}

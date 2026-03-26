package media

import "testing"

func TestCollectURIsFromMap_Deterministic(t *testing.T) {
	m := map[string]any{
		"z_file": "media://z-id",
		"a_file": "media://a-id",
		"m_file": "media://m-id",
		"plain":  "not a media uri",
	}
	uris := CollectURIsFromMap(m)
	expected := []string{"media://a-id", "media://m-id", "media://z-id"}
	if len(uris) != len(expected) {
		t.Fatalf("len = %d, want %d", len(uris), len(expected))
	}
	for i, want := range expected {
		if uris[i] != want {
			t.Errorf("uris[%d] = %q, want %q", i, uris[i], want)
		}
	}
}

func TestCollectURIsFromMap_SliceValues(t *testing.T) {
	m := map[string]any{
		"files": []any{"media://id1", "not-media", "media://id2"},
	}
	uris := CollectURIsFromMap(m)
	if len(uris) != 2 {
		t.Fatalf("len = %d, want 2", len(uris))
	}
	if uris[0] != "media://id1" || uris[1] != "media://id2" {
		t.Errorf("uris = %v", uris)
	}
}

func TestCollectURIsFromMap_StringSliceValues(t *testing.T) {
	m := map[string]any{
		"files": []string{"media://id1", "not-media", "media://id2"},
	}
	uris := CollectURIsFromMap(m)
	if len(uris) != 2 {
		t.Fatalf("len = %d, want 2", len(uris))
	}
}

func TestCollectURIsFromMap_Empty(t *testing.T) {
	uris := CollectURIsFromMap(map[string]any{})
	if len(uris) != 0 {
		t.Errorf("expected empty, got %v", uris)
	}
}

func TestCollectURIsFromMap_Nil(t *testing.T) {
	uris := CollectURIsFromMap(nil)
	if uris != nil {
		t.Errorf("expected nil, got %v", uris)
	}
}

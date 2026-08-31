package common

import "testing"

func TestTns(t *testing.T) {
	expectedSlicePath := []string{"wasm", "http", "path"}
	expectedStringPath := "wasm/http/path"

	p := NewTnsPath(expectedSlicePath)
	if p.String() != expectedStringPath {
		t.Errorf("Expected `%v` got %v", expectedStringPath, p.String())
		return
	}

	for idx, val := range expectedSlicePath {
		if p.Slice()[idx] != val || len(val) == 0 {
			t.Errorf("Expected val `%s` of length greater than 0 got `%s`", val, p.Slice()[idx])
		}
	}
}

package engine

import (
	"errors"
	"testing"
)

func TestKey(t *testing.T) {
	attr := &Attribute{}
	option := Key()
	option(attr)

	if !attr.Key {
		t.Errorf("Expected Key to be true")
	}
}

func TestRequired(t *testing.T) {
	attr := &Attribute{}
	option := Required()
	option(attr)

	if !attr.Required {
		t.Errorf("Expected Required to be true")
	}
}

func TestPathOption(t *testing.T) {
	attr := &Attribute{}
	expectedPath := []StringMatch{"path1", "path2"}
	Path(expectedPath...)(attr)
	if len(attr.Path) != len(expectedPath) {
		t.Fatalf("Expected path length %d, got %d", len(expectedPath), len(attr.Path))
	}
	for i, p := range expectedPath {
		if attr.Path[i] != p {
			t.Errorf("Expected path at index %d to be %s, got %s", i, p, attr.Path[i])
		}
	}
}

func TestCompatOption(t *testing.T) {
	attr := &Attribute{}
	expectedCompat := []StringMatch{"compat1", "compat2"}
	Compat(expectedCompat...)(attr)
	if len(attr.Compat) != len(expectedCompat) {
		t.Fatalf("Expected compat length %d, got %d", len(expectedCompat), len(attr.Compat))
	}
	for i, c := range expectedCompat {
		if attr.Compat[i] != c {
			t.Errorf("Expected compat at index %d to be %s, got %s", i, c, attr.Compat[i])
		}
	}
}

func TestDefaultOption(t *testing.T) {
	attr := &Attribute{}
	expectedDefault := "defaultTest"
	Default(expectedDefault)(attr)
	if attr.Default != expectedDefault {
		t.Errorf("Expected default to be %s, got %v", expectedDefault, attr.Default)
	}
}

func TestValidatorOption(t *testing.T) {
	attr := &Attribute{}
	expectedError := "test error"
	v := func(s string) error {
		return errors.New(expectedError)
	}
	Validator(v)(attr)
	err := attr.Validator("testString")
	if err.Error() != expectedError {
		t.Errorf("Expected error to be %s, got %s", expectedError, err.Error())
	}
	err = attr.Validator(123)
	if err == nil || err.Error() != "invalid type passed to validator" {
		t.Errorf("Expected error for invalid type, got %v", err)
	}
}

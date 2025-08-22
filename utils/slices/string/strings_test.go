package slices_test

import (
	"reflect"
	"testing"

	slices "github.com/taubyte/tau/utils/slices/string"
)

func TestUnique(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "single item slice",
			input:    []string{"one"},
			expected: []string{"one"},
		},
		{
			name:     "multiple items, no duplicates",
			input:    []string{"one", "two", "three"},
			expected: []string{"one", "two", "three"},
		},
		{
			name:     "multiple items, some duplicates",
			input:    []string{"one", "two", "three", "two"},
			expected: []string{"one", "two", "three"},
		},
		{
			name:     "multiple items, all duplicates",
			input:    []string{"one", "one", "one", "one"},
			expected: []string{"one"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := slices.Unique(tt.input); !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("Unique(%v) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

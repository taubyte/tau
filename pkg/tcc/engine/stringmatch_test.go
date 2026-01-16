package engine

import "testing"

func TestStringMatchAll(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"", true},
		{"test", true},
		{"any_string", true},
	}

	sm := StringMatchAll{}
	for _, test := range tests {
		if sm.Match(test.input) != test.expected {
			t.Errorf("Expected StringMatchAll(%s) to be %v, got %v", test.input, test.expected, !test.expected)
		}
	}
}

func TestAll(t *testing.T) {
	matchFunc := All()

	tests := []struct {
		input    string
		expected bool
	}{
		{"", true},
		{"test", true},
		{"any_string", true},
	}

	for _, test := range tests {
		if matchFunc.Match(test.input) != test.expected {
			t.Errorf("Expected All()(%s) to be %v, got %v", test.input, test.expected, !test.expected)
		}
	}
}

func TestEither(t *testing.T) {
	tests := []struct {
		values   []string
		input    string
		expected bool
	}{
		{[]string{"apple", "banana", "cherry"}, "apple", true},
		{[]string{"apple", "banana", "cherry"}, "pear", false},
		{[]string{}, "any_string", false},
		{[]string{"test"}, "test", true},
		{[]string{"test"}, "testing", false},
	}

	for _, test := range tests {
		matchFunc := Either(test.values...)
		if matchFunc.Match(test.input) != test.expected {
			t.Errorf("Expected Either(%v)(%s) to be %v, got %v", test.values, test.input, test.expected, !test.expected)
		}
	}
}

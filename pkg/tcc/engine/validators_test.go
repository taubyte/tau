package engine

import (
	"testing"
)

func TestIsVariableName(t *testing.T) {
	attr := &Attribute{}
	option := IsVariableName()
	option(attr)

	tests := []struct {
		val     string
		isValid bool
	}{
		{"validVarName", true},
		{"_validVarName", true},
		{"2invalidVarName", false},
		{"validVarName!", false},
		{"ValidVarName", true},
		{"valid-Var-Name", false},
	}

	for _, test := range tests {
		err := attr.Validator(test.val)
		if test.isValid && err != nil {
			t.Errorf("Expected variable name '%s' to be valid, but got error: %v", test.val, err)
		}
		if !test.isValid && err == nil {
			t.Errorf("Expected variable name '%s' to be invalid, but got no error", test.val)
		}
	}
}

func TestIsCID(t *testing.T) {
	attr := &Attribute{}
	option := IsCID()
	option(attr)

	tests := []struct {
		val     string
		isValid bool
	}{
		// A valid CID
		{"QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG", true},
		// An invalid CID
		{"invalidCID", false},
	}

	for _, test := range tests {
		err := attr.Validator(test.val)
		if test.isValid && err != nil {
			t.Errorf("Expected CID '%s' to be valid, but got error: %v", test.val, err)
		}
		if !test.isValid && err == nil {
			t.Errorf("Expected CID '%s' to be invalid, but got no error", test.val)
		}
	}
}

func TestIsEmail(t *testing.T) {
	attr := &Attribute{}
	option := IsEmail()
	option(attr)

	tests := []struct {
		val     string
		isValid bool
	}{
		{"test@example.com", true},
		{"invalid-email", false},
	}

	for _, test := range tests {
		err := attr.Validator(test.val)
		if test.isValid && err != nil {
			t.Errorf("Expected email '%s' to be valid, but got error: %v", test.val, err)
		}
		if !test.isValid && err == nil {
			t.Errorf("Expected email '%s' to be invalid, but got no error", test.val)
		}
	}
}

func TestInSet(t *testing.T) {
	attr := &Attribute{}
	option := InSet("test", "sample")
	option(attr)

	tests := []struct {
		val     string
		isValid bool
	}{
		{"test", true},
		{"notInSet", false},
	}

	for _, test := range tests {
		err := attr.Validator(test.val)
		if test.isValid && err != nil {
			t.Errorf("Expected value '%s' to be in set, but got error: %v", test.val, err)
		}
		if !test.isValid && err == nil {
			t.Errorf("Expected value '%s' not to be in set, but got no error", test.val)
		}
	}
}

func TestIsFqdn(t *testing.T) {
	attr := &Attribute{}
	option := IsFqdn()
	option(attr)

	tests := []struct {
		val     string
		isValid bool
	}{
		{"example.com", true},
		{"subdomain.example.com", true},
		{"just_a_string", true},
		{".dotprefix.com", false},
		{"double..dot.com", false},
		{".start.dot.com", false},
		{"dotends.com.", true},
	}

	for _, test := range tests {
		err := attr.Validator(test.val)
		if test.isValid && err != nil {
			t.Errorf("Expected domain '%s' to be valid, but got error: %v", test.val, err)
		}
		if !test.isValid && err == nil {
			t.Errorf("Expected domain '%s' to be invalid, but got no error", test.val)
		}
	}
}

func TestIsHttpMethod(t *testing.T) {
	attr := &Attribute{}
	option := IsHttpMethod()
	option(attr)

	tests := []struct {
		val     string
		isValid bool
	}{
		{"GET", true},
		{"HEAD", true},
		{"INVALID", false},
		{"http", false},
	}

	for _, test := range tests {
		err := attr.Validator(test.val)
		if test.isValid && err != nil {
			t.Errorf("Expected method '%s' to be valid, but got error: %v", test.val, err)
		}
		if !test.isValid && err == nil {
			t.Errorf("Expected method '%s' to be invalid, but got no error", test.val)
		}
	}
}

package validate_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/validate"
	"gotest.tools/v3/assert"
)

func TestFQDNValidator(t *testing.T) {
	assert.NilError(t, validate.FQDNValidator(""))
	assert.NilError(t, validate.FQDNValidator("example.com"))
	assert.NilError(t, validate.FQDNValidator("sub.example.com"))
	err := validate.FQDNValidator("invalid..name")
	assert.ErrorContains(t, err, "invalid")
}

func TestSliceContains(t *testing.T) {
	slice := []string{"a", "b", "c"}
	assert.Equal(t, validate.SliceContains(slice, "a"), true)
	assert.Equal(t, validate.SliceContains(slice, "b"), true)
	assert.Equal(t, validate.SliceContains(slice, "d"), false)
	assert.Equal(t, validate.SliceContains(nil, "a"), false)
	assert.Equal(t, validate.SliceContains([]string{}, "a"), false)
}

func TestVariablePathValidator(t *testing.T) {
	assert.NilError(t, validate.VariablePathValidator(""))
	assert.NilError(t, validate.VariablePathValidator("/path"))
	assert.NilError(t, validate.VariablePathValidator("/"))
	err := validate.VariablePathValidator("no-leading-slash")
	assert.ErrorContains(t, err, "start with")
}

func TestVariableTime(t *testing.T) {
	assert.NilError(t, validate.VariableTime(""))
	assert.NilError(t, validate.VariableTime("1s"))
	assert.NilError(t, validate.VariableTime("5m"))
	err := validate.VariableTime("invalid")
	assert.ErrorContains(t, err, "invalid time")
}

func TestVariableBool(t *testing.T) {
	assert.NilError(t, validate.VariableBool(""))
	assert.NilError(t, validate.VariableBool("true"))
	assert.NilError(t, validate.VariableBool("false"))
	err := validate.VariableBool("maybe")
	assert.ErrorContains(t, err, "invalid")
}

func TestVariableProviderValidator(t *testing.T) {
	assert.NilError(t, validate.VariableProviderValidator(""))
	assert.NilError(t, validate.VariableProviderValidator("github"))
	assert.NilError(t, validate.VariableProviderValidator("GITHUB"))
	err := validate.VariableProviderValidator("gitlab")
	assert.ErrorContains(t, err, "not supported")
}

func TestVariableIntValidator(t *testing.T) {
	assert.NilError(t, validate.VariableIntValidator(""))
	assert.NilError(t, validate.VariableIntValidator("0"))
	assert.NilError(t, validate.VariableIntValidator("42"))
	err := validate.VariableIntValidator("x")
	assert.ErrorContains(t, err, "integer")
}

func TestVariableNameValidator(t *testing.T) {
	assert.NilError(t, validate.VariableNameValidator(""))
	assert.NilError(t, validate.VariableNameValidator("a"))
	assert.NilError(t, validate.VariableNameValidator("abc_123"))
	err := validate.VariableNameValidator("1abc")
	assert.ErrorContains(t, err, "Must start with a letter")
}

func TestVariableDescriptionValidator(t *testing.T) {
	assert.NilError(t, validate.VariableDescriptionValidator(""))
	assert.NilError(t, validate.VariableDescriptionValidator("short"))
	long := string(make([]byte, 251))
	err := validate.VariableDescriptionValidator(long)
	assert.ErrorContains(t, err, "250")
}

func TestVariableTagsValidator(t *testing.T) {
	assert.NilError(t, validate.VariableTagsValidator(nil))
	assert.NilError(t, validate.VariableTagsValidator([]string{}))
	assert.NilError(t, validate.VariableTagsValidator([]string{"a"}))
}

func TestVariableRequiredValidator(t *testing.T) {
	assert.NilError(t, validate.VariableRequiredValidator(""))
	assert.NilError(t, validate.VariableRequiredValidator("x"))
	long := string(make([]byte, 251))
	err := validate.VariableRequiredValidator(long)
	assert.ErrorContains(t, err, "250")
}

func TestRequiredNoCharLimit(t *testing.T) {
	assert.NilError(t, validate.RequiredNoCharLimit(""))
	assert.NilError(t, validate.RequiredNoCharLimit("x"))
}

func TestVariableSizeValidator(t *testing.T) {
	assert.NilError(t, validate.VariableSizeValidator(""))
	assert.NilError(t, validate.VariableSizeValidator("42"))
	assert.NilError(t, validate.VariableSizeValidator("10GB"))
	err := validate.VariableSizeValidator("invalid")
	assert.ErrorContains(t, err, "invalid size")
}

func TestMethodTypeValidator(t *testing.T) {
	assert.NilError(t, validate.MethodTypeValidator(""))
	assert.NilError(t, validate.MethodTypeValidator("http"))
	assert.NilError(t, validate.MethodTypeValidator("pubsub"))
	err := validate.MethodTypeValidator("invalid")
	assert.ErrorContains(t, err, "invalid")
}

func TestCodeTypeValidator(t *testing.T) {
	assert.NilError(t, validate.CodeTypeValidator(""))
	// Valid values match constants.CodeTypes after ToLower; slice is [Go AssemblyScript] so exact match needed
	err := validate.CodeTypeValidator("rust")
	assert.ErrorContains(t, err, "invalid")
}

func TestBucketTypeValidator(t *testing.T) {
	assert.NilError(t, validate.BucketTypeValidator(""))
	assert.NilError(t, validate.BucketTypeValidator("Object"))
	assert.NilError(t, validate.BucketTypeValidator("Streaming"))
	err := validate.BucketTypeValidator("invalid")
	assert.ErrorContains(t, err, "invalid")
}

func TestApiMethodValidator(t *testing.T) {
	assert.NilError(t, validate.ApiMethodValidator(""))
	err := validate.ApiMethodValidator("invalid")
	assert.ErrorContains(t, err, "invalid")
}

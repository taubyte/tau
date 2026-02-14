package utils

import (
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"gotest.tools/v3/assert"
)

func TestValidateMinMax_Valid(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("db")
	sel.Set("min", 1)
	sel.Set("max", 3)

	err := ValidateMinMax(sel, "min", "max")
	assert.NilError(t, err)
}

func TestValidateMinMax_Equal(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("db")
	sel.Set("min", 2)
	sel.Set("max", 2)

	err := ValidateMinMax(sel, "min", "max")
	assert.NilError(t, err)
}

func TestValidateMinMax_MaxLessThanMin(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("db")
	sel.Set("min", 5)
	sel.Set("max", 2)

	err := ValidateMinMax(sel, "min", "max")
	assert.ErrorContains(t, err, "max (2) must be >= min (5)")
}

func TestValidateMinMax_MissingMinKey(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("db")
	sel.Set("max", 3)

	err := ValidateMinMax(sel, "min", "max")
	assert.NilError(t, err)
}

func TestValidateMinMax_MissingMaxKey(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("db")
	sel.Set("min", 1)

	err := ValidateMinMax(sel, "min", "max")
	assert.NilError(t, err)
}

func TestValidateMinMax_BothMissing(t *testing.T) {
	obj := object.New[object.Refrence]()
	sel := obj.Child("db")

	err := ValidateMinMax(sel, "min", "max")
	assert.NilError(t, err)
}

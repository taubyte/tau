package common_test

import (
	"fmt"
	"log"
	"testing"

	"github.com/alecthomas/units"
	"github.com/taubyte/tau/pkg/schema/common"
	"gotest.tools/v3/assert"
)

func TestUnitsError(t *testing.T) {
	// Empty
	unit, err := common.StringToUnits("")
	assert.NilError(t, err)
	assert.Assert(t, unit == 0)

	_, err = common.StringToUnits("not a unit")
	assert.ErrorContains(t, err, "units: invalid not a unit")
}

func ExampleUnitsToString() {

	unit := common.UnitsToString(64 * units.MB)
	fmt.Println(unit)

	unit = common.UnitsToString(64*units.MB + 10*units.KB)
	fmt.Println(unit)

	// Output: 64MB
	// 64MB10KB
}

func ExampleStringToUnits() {
	size, err := common.StringToUnits("64MB")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(size)

	size, err = common.StringToUnits("64MB10KB")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(size)

	// Output: 64000000
	// 64010000
}

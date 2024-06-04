package common_test

import (
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/taubyte/tau/pkg/schema/common"
	"gotest.tools/v3/assert"
)

func TestTimeError(t *testing.T) {
	_, err := common.StringToTime("not a time")
	assert.ErrorContains(t, err, `invalid duration "not a time"`)
}

func ExampleTimeToString() {
	unit := common.TimeToString(3 * time.Hour)
	fmt.Println(unit)

	unit = common.TimeToString(3*time.Hour + 10*time.Minute)
	fmt.Println(unit)

	unit = common.TimeToString(10 * time.Minute)
	fmt.Println(unit)

	unit = common.TimeToString(15*time.Millisecond + 10*time.Nanosecond)
	fmt.Println(unit)

	unit = common.TimeToString(10 * time.Microsecond)
	fmt.Println(unit)

	// Output: 3h
	// 3h10m
	// 10m
	// 15.00001ms
	// 10µs
}

func ExampleStringToTime() {
	unit, err := common.StringToTime("3h")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(unit)

	unit, err = common.StringToTime("3h10m")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(unit)

	unit, err = common.StringToTime("10m")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(unit)

	unit, err = common.StringToTime("5000ns")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(unit)

	unit, err = common.StringToTime("")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(unit)

	// Output: 10800000000000
	// 11400000000000
	// 600000000000
	// 5000
	// 0
}

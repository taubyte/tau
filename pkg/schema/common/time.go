package common

import (
	"log"
	"regexp"
	"time"

	"golang.org/x/exp/constraints"
)

var removeSubTimes *regexp.Regexp

func init() {
	var err error
	removeSubTimes, err = regexp.Compile(`^([1-9][0-9]*h)?([1-9][0-9]*m)?([1-9][0-9]*s)?`)
	if err != nil {
		log.Fatal(err)
	}
}

// TimeToString Converts _time a unit64 representing nanoseconds to a unit in form 1h10m43s
func TimeToString[T constraints.Integer | time.Duration](_time T) string {
	dur := time.Duration(_time)

	if dur < time.Second {
		return dur.String()
	}

	// converts 1h0m0s to 1h
	return removeSubTimes.FindString(dur.String())
}

// StringToTime Converts _time a unit in form 1h10m43s to a uint64 representing nanoseconds
func StringToTime(_time string) (uint64, error) {
	if _time == "" {
		return 0, nil
	}

	duration, err := time.ParseDuration(_time)
	if err != nil {
		return 0, err
	}

	return uint64(duration), nil
}

package validate

import (
	"strconv"

	"github.com/taubyte/go-project-schema/common"
)

func IsAny(val string, tests ...func(val string) bool) bool {
	for _, test := range tests {
		if test(val) {
			return true
		}
	}
	return false
}

func IsInt(val string) bool {
	if _, err := strconv.Atoi(val); err == nil {
		return true
	}
	return false
}

func IsBytes(val string) bool {
	if _, err := common.StringToUnits(val); err == nil {
		return true
	}
	return false
}

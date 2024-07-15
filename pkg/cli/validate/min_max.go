package validate

import (
	"fmt"
	"strconv"
)

func VariableMinValidator(val string) error {
	if len(val) == 0 {
		return nil
	}

	if _, err := strconv.Atoi(val); err != nil {
		return fmt.Errorf(InvalidMinValue, val, err)
	}

	return nil
}

func VariableMaxValidator(val string) error {
	if len(val) == 0 {
		return nil
	}

	if _, err := strconv.Atoi(val); err != nil {
		return fmt.Errorf(InvalidMaxValue, val, err)
	}

	return nil
}
